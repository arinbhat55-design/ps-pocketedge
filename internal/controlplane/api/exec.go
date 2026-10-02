package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// handleContainerExec relays an interactive `docker exec` session over a
// WebSocket: binary frames from the browser are stdin, binary frames sent
// back are pty output (stdout+stderr, already combined by the pty on the
// agent side — see docker.StartExec's Tty:true), and a text frame from the
// browser of the form "resize:<cols>x<rows>" resizes the pty. Auth via
// ?token=, the same documented exception every WS endpoint in this
// package uses (see handleServerStream's doc comment).
func handleContainerExec(log *slog.Logger, authMgr *auth.Manager, dispatcher *deploy.Dispatcher, relay *deploy.ExecStreamRelay) http.HandlerFunc {
	return handleExec(log, authMgr, dispatcher, relay, false)
}

// handleHostExec shares the admin-only authenticated relay, but explicitly
// targets a host process instead of a container.
func handleHostExec(log *slog.Logger, authMgr *auth.Manager, dispatcher *deploy.Dispatcher, relay *deploy.ExecStreamRelay) http.HandlerFunc {
	return handleExec(log, authMgr, dispatcher, relay, true)
}

func handleExec(log *slog.Logger, authMgr *auth.Manager, dispatcher *deploy.Dispatcher, relay *deploy.ExecStreamRelay, hostShell bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, err := authMgr.AuthenticateRequest(r, streamToken(r))
		if err != nil {
			auth.WriteAuthError(w, err)
			return
		}
		if claims.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")
		if hostShell {
			containerID = ""
		} else if containerID == "" {
			http.Error(w, "container is required", http.StatusBadRequest)
			return
		}
		// Repeated cmd parameters preserve argument boundaries for terminal
		// clients. Omitting them retains the dashboard's default shell.
		command, err := parseExecCommand(r.URL.Query()["cmd"])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		cols, rows := 80, 24
		if v := r.URL.Query().Get("cols"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cols = n
			}
		}
		if v := r.URL.Query().Get("rows"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				rows = n
			}
		}

		requestID, err := auth.RandomToken()
		if err != nil {
			log.Error("failed to generate exec request id", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Subscribe before dispatching ExecStartCommand so no output chunk
		// sent right after the agent starts the process is missed — same
		// ordering every other stream subscription in this package uses.
		ch, unsubscribe := relay.Subscribe(requestID)
		defer unsubscribe()

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_ExecStart{
				ExecStart: &agentv1.ExecStartCommand{
					RequestId:   requestID,
					ServerId:    serverID,
					ContainerId: containerID,
					HostShell:   hostShell,
					Cmd:         command,
					Cols:        uint32(cols),
					Rows:        uint32(rows),
				},
			},
		}
		if err := dispatcher.Send(serverID, cmd); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}
		stop := func() {
			_ = dispatcher.Send(serverID, &agentv1.ControlMessage{
				Payload: &agentv1.ControlMessage_StopStream{StopStream: &agentv1.StopStreamCommand{RequestId: requestID}},
			})
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			stop()
			log.Warn("failed to upgrade exec stream", "server_id", serverID, "container_id", containerID, "error", err)
			return
		}
		defer conn.Close()
		defer stop()

		// Reader goroutine: browser -> agent. Binary frames are stdin
		// bytes; text frames carry a resize request.
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				msgType, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				input := &agentv1.ExecInputCommand{RequestId: requestID}
				if msgType == websocket.TextMessage {
					cols, rows, ok := parseResize(string(data))
					if !ok {
						continue
					}
					input.ResizeCols, input.ResizeRows = cols, rows
				} else {
					input.Data = data
				}
				_ = dispatcher.Send(serverID, &agentv1.ControlMessage{
					Payload: &agentv1.ControlMessage_ExecInput{ExecInput: input},
				})
			}
		}()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-readDone:
				return
			case chunk, ok := <-ch:
				if !ok {
					return
				}
				if len(chunk.GetData()) > 0 {
					if err := conn.WriteMessage(websocket.BinaryMessage, chunk.GetData()); err != nil {
						return
					}
				}
				if chunk.GetDone() {
					_ = conn.WriteJSON(map[string]any{
						"done":     true,
						"exitCode": chunk.GetExitCode(),
						"error":    chunk.GetErrorMessage(),
					})
					return
				}
			}
		}
	}
}

func parseExecCommand(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	if len(args) > 128 || args[0] == "" {
		return nil, fmt.Errorf("exec requires a nonempty executable and at most 128 arguments")
	}
	total := 0
	for _, arg := range args {
		total += len(arg)
		if strings.ContainsRune(arg, '\x00') || total > 16384 {
			return nil, fmt.Errorf("exec command contains a NUL or exceeds 16 KiB")
		}
	}
	return args, nil
}

// CLI clients can send a bearer header; browsers retain query-token auth.
func streamToken(r *http.Request) string {
	if header := r.Header.Get("Authorization"); header != "" {
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok {
			return ""
		}
		return token
	}
	return r.URL.Query().Get("token")
}

// parseResize parses a "resize:<cols>x<rows>" control message sent by the
// Flutter terminal widget whenever its viewport size changes.
func parseResize(s string) (cols, rows uint32, ok bool) {
	const prefix = "resize:"
	if !strings.HasPrefix(s, prefix) {
		return 0, 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(s, prefix), "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	c, err1 := strconv.Atoi(parts[0])
	rw, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || c <= 0 || rw <= 0 {
		return 0, 0, false
	}
	return uint32(c), uint32(rw), true
}
