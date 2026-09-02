package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/ai"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// logFetchTimeout bounds a bounded (follow=false) log fetch, used by both
// download and AI analysis — generous for a Docker Engine API round trip
// plus network transfer of a few thousand lines.
const logFetchTimeout = 30 * time.Second

// logDownloadDefaultTail/aiAnalysisTail cap how much history is pulled
// when the client doesn't ask for a specific range — download defaults to
// a larger window than AI analysis, which only needs enough recent
// context for a summary, not a full audit trail.
const (
	logDownloadDefaultTail = 2000
	aiAnalysisTail         = 500
)

type logLineJSON struct {
	ContainerID       string `json:"containerId"`
	TimestampUnixNano int64  `json:"timestampUnixNano"`
	Stream            string `json:"stream"`
	Message           string `json:"message"`
}

type logChunkJSON struct {
	ContainerID string        `json:"containerId"`
	Lines       []logLineJSON `json:"lines"`
	Done        bool          `json:"done"`
	Error       string        `json:"error,omitempty"`
}

type containerEventJSON struct {
	TimestampUnix int64  `json:"timestampUnix"`
	Type          string `json:"type"`
	Action        string `json:"action"`
	ContainerID   string `json:"containerId"`
	ContainerName string `json:"containerName"`
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// parseLogRangeParams reads the ?tail=&since=&until= query params shared
// by the download, analyze, and live-tail endpoints. since/until are
// RFC3339; a parse failure writes a 400 and returns ok=false.
func parseLogRangeParams(w http.ResponseWriter, r *http.Request, defaultTail int) (tail int, since, until time.Time, ok bool) {
	q := r.URL.Query()
	tail = defaultTail
	if v := q.Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			http.Error(w, "invalid tail", http.StatusBadRequest)
			return 0, time.Time{}, time.Time{}, false
		}
		tail = n
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid since", http.StatusBadRequest)
			return 0, time.Time{}, time.Time{}, false
		}
		since = t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid until", http.StatusBadRequest)
			return 0, time.Time{}, time.Time{}, false
		}
		until = t
	}
	return tail, since, until, true
}

// fetchContainerLogs dispatches a non-follow StreamLogsCommand and
// collects every LogChunk until the agent reports done, within timeout —
// the bounded-fetch counterpart to handleContainerLogsStream's live tail,
// shared by download and AI analysis.
func fetchContainerLogs(dispatcher *deploy.Dispatcher, relay *deploy.LogStreamRelay, serverID, containerID string, tailLines int, since, until time.Time, timeout time.Duration) ([]logLineJSON, error) {
	requestID, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}

	ch, cleanup := relay.Subscribe(requestID)
	defer cleanup()

	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_StreamLogs{
			StreamLogs: &agentv1.StreamLogsCommand{
				RequestId:   requestID,
				ServerId:    serverID,
				ContainerId: containerID,
				Follow:      false,
				TailLines:   int32(tailLines),
				SinceUnix:   unixOrZero(since),
				UntilUnix:   unixOrZero(until),
			},
		},
	}
	if err := dispatcher.Send(serverID, cmd); err != nil {
		return nil, err
	}

	var lines []logLineJSON
	deadline := time.After(timeout)
	for {
		select {
		case chunk := <-ch:
			for _, l := range chunk.GetLines() {
				lines = append(lines, logLineJSON{
					ContainerID:       containerID,
					TimestampUnixNano: l.GetTimestampUnixNano(),
					Stream:            l.GetStream(),
					Message:           l.GetMessage(),
				})
			}
			if chunk.GetDone() {
				if chunk.GetErrorMessage() != "" {
					return lines, errors.New(chunk.GetErrorMessage())
				}
				return lines, nil
			}
		case <-deadline:
			return lines, errOpTimeout
		}
	}
}

// handleContainerLogsStream pushes a live (or bounded, if ?follow=false)
// tail of one or more containers' logs over a WebSocket, merging multiple
// containers (?with=id2,id3 — "combine logs from related containers",
// typically every container in the same deployment) into one interleaved
// feed. Auth via ?token=, the same documented exception every WS endpoint
// in this package uses (see handleServerStream's doc comment).
func handleContainerLogsStream(log *slog.Logger, authMgr *auth.Manager, dispatcher *deploy.Dispatcher, relay *deploy.LogStreamRelay) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := authMgr.ParseToken(r.URL.Query().Get("token")); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		serverID := r.PathValue("id")
		containerIDs := []string{r.PathValue("containerId")}
		if with := r.URL.Query().Get("with"); with != "" {
			for _, id := range strings.Split(with, ",") {
				if id != "" {
					containerIDs = append(containerIDs, id)
				}
			}
		}

		q := r.URL.Query()
		follow := q.Get("follow") != "false"
		tail, since, until, ok := parseLogRangeParams(w, r, 200)
		if !ok {
			return
		}

		type sub struct {
			requestID   string
			ch          chan *agentv1.LogChunk
			unsubscribe func()
		}
		subs := make([]sub, 0, len(containerIDs))
		for range containerIDs {
			requestID, err := auth.RandomToken()
			if err != nil {
				log.Error("failed to generate log stream request id", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			ch, unsubscribe := relay.Subscribe(requestID)
			subs = append(subs, sub{requestID: requestID, ch: ch, unsubscribe: unsubscribe})
		}
		defer func() {
			for _, s := range subs {
				s.unsubscribe()
			}
		}()

		stopAll := func() {
			for _, s := range subs {
				_ = dispatcher.Send(serverID, &agentv1.ControlMessage{
					Payload: &agentv1.ControlMessage_StopStream{StopStream: &agentv1.StopStreamCommand{RequestId: s.requestID}},
				})
			}
		}

		for i, cid := range containerIDs {
			cmd := &agentv1.ControlMessage{
				Payload: &agentv1.ControlMessage_StreamLogs{
					StreamLogs: &agentv1.StreamLogsCommand{
						RequestId:   subs[i].requestID,
						ServerId:    serverID,
						ContainerId: cid,
						Follow:      follow,
						TailLines:   int32(tail),
						SinceUnix:   unixOrZero(since),
						UntilUnix:   unixOrZero(until),
					},
				},
			}
			if err := dispatcher.Send(serverID, cmd); err != nil {
				stopAll()
				writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
				return
			}
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			stopAll()
			log.Warn("failed to upgrade logs stream", "server_id", serverID, "error", err)
			return
		}
		defer conn.Close()
		defer stopAll()

		closed := make(chan struct{})
		go func() {
			for {
				if _, _, err := conn.NextReader(); err != nil {
					close(closed)
					return
				}
			}
		}()

		merged := make(chan *agentv1.LogChunk, 64)
		var wg sync.WaitGroup
		for _, s := range subs {
			wg.Add(1)
			go func(s sub) {
				defer wg.Done()
				for {
					select {
					case chunk, ok := <-s.ch:
						if !ok {
							return
						}
						select {
						case merged <- chunk:
						case <-closed:
							return
						case <-r.Context().Done():
							return
						}
						if chunk.GetDone() {
							return
						}
					case <-closed:
						return
					case <-r.Context().Done():
						return
					}
				}
			}(s)
		}
		go func() {
			wg.Wait()
			close(merged)
		}()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-closed:
				return
			case chunk, ok := <-merged:
				if !ok {
					return
				}
				lines := make([]logLineJSON, len(chunk.GetLines()))
				for i, l := range chunk.GetLines() {
					lines[i] = logLineJSON{
						ContainerID:       chunk.GetContainerId(),
						TimestampUnixNano: l.GetTimestampUnixNano(),
						Stream:            l.GetStream(),
						Message:           l.GetMessage(),
					}
				}
				out := logChunkJSON{ContainerID: chunk.GetContainerId(), Lines: lines, Done: chunk.GetDone(), Error: chunk.GetErrorMessage()}
				if err := conn.WriteJSON(out); err != nil {
					return
				}
			}
		}
	}
}

// handleDownloadContainerLogs returns a bounded log dump as a plain-text
// attachment — the "download logs" action, and also usable for "share"
// (the client can hand the downloaded file to the OS share sheet).
func handleDownloadContainerLogs(log *slog.Logger, dispatcher *deploy.Dispatcher, relay *deploy.LogStreamRelay) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		tail, since, until, ok := parseLogRangeParams(w, r, logDownloadDefaultTail)
		if !ok {
			return
		}

		lines, err := fetchContainerLogs(dispatcher, relay, serverID, containerID, tail, since, until, logFetchTimeout)
		if err != nil {
			if errors.Is(err, deploy.ErrAgentNotConnected) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
				return
			}
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+containerID+`-logs.txt"`)
		w.WriteHeader(http.StatusOK)
		for _, l := range lines {
			ts := "?"
			if l.TimestampUnixNano > 0 {
				ts = time.Unix(0, l.TimestampUnixNano).Format(time.RFC3339Nano)
			}
			fmt.Fprintf(w, "%s [%s] %s\n", ts, l.Stream, l.Message)
		}
	}
}

// handleListContainerEvents serves a bounded history of Docker events for
// one container (or, per ListEventsCommand's doc comment, every container
// on the server if containerId were empty — not exposed at this route,
// which is always scoped to one container).
func handleListContainerEvents(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.EventListWaiter) http.HandlerFunc {
	const eventsTimeout = 15 * time.Second

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		_, since, until, ok := parseLogRangeParams(w, r, 0)
		if !ok {
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		ch, cleanup := waiter.Await(requestID)
		defer cleanup()

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_ListEvents{
				ListEvents: &agentv1.ListEventsCommand{
					RequestId:   requestID,
					ServerId:    serverID,
					ContainerId: containerID,
					SinceUnix:   unixOrZero(since),
					UntilUnix:   unixOrZero(until),
				},
			},
		}
		if err := dispatcher.Send(serverID, cmd); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}

		select {
		case result := <-ch:
			if result.GetErrorMessage() != "" {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": result.GetErrorMessage()})
				return
			}
			out := make([]containerEventJSON, len(result.GetEvents()))
			for i, e := range result.GetEvents() {
				out[i] = containerEventJSON{
					TimestampUnix: e.GetTimestampUnix(),
					Type:          e.GetType(),
					Action:        e.GetAction(),
					ContainerID:   e.GetContainerId(),
					ContainerName: e.GetContainerName(),
				}
			}
			writeJSON(w, http.StatusOK, out)
		case <-time.After(eventsTimeout):
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for agent response"})
		case <-r.Context().Done():
		}
	}
}

// handleAIStatus tells the Flutter app up front whether AI log-analysis
// is available, so it can hide the button entirely instead of only
// failing on click.
func handleAIStatus(aiClient *ai.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"configured": aiClient.Configured()})
	}
}

// handleAnalyzeContainerLogs fetches a bounded recent log window plus
// whatever container context the store has (image, status) and asks
// ai.Client for a summary/root-cause/recommendation.
func handleAnalyzeContainerLogs(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, relay *deploy.LogStreamRelay, aiClient *ai.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !aiClient.Configured() {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": ai.ErrNotConfigured.Error()})
			return
		}

		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		lines, err := fetchContainerLogs(dispatcher, relay, serverID, containerID, aiAnalysisTail, time.Time{}, time.Time{}, logFetchTimeout)
		if err != nil {
			if errors.Is(err, deploy.ErrAgentNotConnected) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
				return
			}
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
			return
		}
		if len(lines) == 0 {
			writeJSON(w, http.StatusOK, ai.Analysis{Summary: "No log output available for this container yet."})
			return
		}

		var logText strings.Builder
		for _, l := range lines {
			fmt.Fprintf(&logText, "[%s] %s\n", l.Stream, l.Message)
		}

		containerContext := "container_id=" + containerID
		if state, err := findContainerState(r.Context(), st, serverID, containerID); err == nil && state != nil {
			containerContext = fmt.Sprintf("image=%s status=%q state=%s", state.Image, state.Status, state.State)
		}

		analysis, err := aiClient.AnalyzeLogs(r.Context(), containerContext, logText.String())
		if err != nil {
			log.Error("AI log analysis failed", "server_id", serverID, "container_id", containerID, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "AI analysis failed: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, analysis)
	}
}
