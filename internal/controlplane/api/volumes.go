package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// volumeListTimeout/volumeDetailTimeout/volumeOpTimeout mirror their image
// counterparts (images.go) — a single Docker Engine API round trip relayed
// over an already-open gRPC stream.
const (
	volumeListTimeout   = 10 * time.Second
	volumeDetailTimeout = 10 * time.Second
	volumeOpTimeout     = 30 * time.Second
)

// volumeSummaryResponse is one volume in a list/detail response, with the
// owning server attached (list) and Orphaned derived from InUseBy — the
// only place orphan status is computed (the agent supplies the raw
// InUseBy, see internal/agent/docker/volumes.go).
type volumeSummaryResponse struct {
	ServerID    string            `json:"serverId,omitempty"`
	ServerName  string            `json:"serverName,omitempty"`
	Name        string            `json:"name"`
	Driver      string            `json:"driver"`
	Mountpoint  string            `json:"mountpoint"`
	Labels      map[string]string `json:"labels,omitempty"`
	SizeBytes   int64             `json:"sizeBytes"`
	InUseBy     []string          `json:"inUseBy,omitempty"`
	Orphaned    bool              `json:"orphaned"`
	CreatedUnix int64             `json:"createdUnix"`
}

func volumeSummaryToResponse(serverID, serverName string, v *agentv1.VolumeSummary) volumeSummaryResponse {
	return volumeSummaryResponse{
		ServerID:    serverID,
		ServerName:  serverName,
		Name:        v.GetName(),
		Driver:      v.GetDriver(),
		Mountpoint:  v.GetMountpoint(),
		Labels:      v.GetLabels(),
		SizeBytes:   v.GetSizeBytes(),
		InUseBy:     v.GetInUseBy(),
		Orphaned:    len(v.GetInUseBy()) == 0,
		CreatedUnix: v.GetCreatedUnix(),
	}
}

// handleListVolumes serves the volume inventory for one server (?serverId=)
// or, if omitted, fans out ListVolumesCommand to every server concurrently
// (same pattern as handleListImages/handleListNetworks) and merges the
// results.
func handleListVolumes(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeListWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.URL.Query().Get("serverId")

		var targets []store.Server
		if serverID != "" {
			srv, err := st.GetServer(r.Context(), serverID)
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "server not found", http.StatusNotFound)
				return
			}
			if err != nil {
				log.Error("failed to load server", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			targets = []store.Server{*srv}
		} else {
			all, err := st.ListServers(r.Context())
			if err != nil {
				log.Error("failed to list servers", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			targets = all
		}

		results := make([][]volumeSummaryResponse, len(targets))
		var wg sync.WaitGroup
		for i, srv := range targets {
			wg.Add(1)
			go func(i int, srv store.Server) {
				defer wg.Done()
				volumes, err := fetchServerVolumes(dispatcher, waiter, srv.ID)
				if err != nil {
					return
				}
				out := make([]volumeSummaryResponse, len(volumes))
				for j, v := range volumes {
					out[j] = volumeSummaryToResponse(srv.ID, srv.Name, v)
				}
				results[i] = out
			}(i, srv)
		}
		wg.Wait()

		merged := []volumeSummaryResponse{}
		for _, r := range results {
			merged = append(merged, r...)
		}
		writeJSON(w, http.StatusOK, merged)
	}
}

func fetchServerVolumes(dispatcher *deploy.Dispatcher, waiter *deploy.VolumeListWaiter, serverID string) ([]*agentv1.VolumeSummary, error) {
	requestID, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}

	ch, cleanup := waiter.Await(requestID)
	defer cleanup()

	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_ListVolumes{
			ListVolumes: &agentv1.ListVolumesCommand{RequestId: requestID, ServerId: serverID},
		},
	}
	if err := dispatcher.Send(serverID, cmd); err != nil {
		return nil, err
	}

	select {
	case result := <-ch:
		return result.GetVolumes(), nil
	case <-time.After(volumeListTimeout):
		return nil, errOpTimeout
	}
}

// handleInspectVolume dispatches an InspectVolumeCommand to the target
// agent and blocks up to volumeDetailTimeout for its VolumeDetail reply —
// same dispatch+correlate+timeout shape as handleInspectImage.
func handleInspectVolume(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeDetailWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		name := r.PathValue("name")

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		ch, cleanup := waiter.Await(requestID)
		defer cleanup()

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_InspectVolume{
				InspectVolume: &agentv1.InspectVolumeCommand{RequestId: requestID, ServerId: serverID, Name: name},
			},
		}
		if err := dispatcher.Send(serverID, cmd); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}

		select {
		case detail := <-ch:
			if !detail.GetFound() {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": detail.GetErrorMessage()})
				return
			}
			writeJSON(w, http.StatusOK, volumeSummaryToResponse(serverID, "", detail.GetVolume()))
		case <-time.After(volumeDetailTimeout):
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for agent response"})
		case <-r.Context().Done():
		}
	}
}

// volumeOpResponse is the JSON shape for a create/remove result.
type volumeOpResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Name    string `json:"name,omitempty"`
}

// sendAndAwaitVolumeOp mirrors sendAndAwaitImageOp.
func sendAndAwaitVolumeOp(dispatcher *deploy.Dispatcher, waiter *deploy.VolumeOpWaiter, serverID, requestID string, cmd *agentv1.ControlMessage, timeout time.Duration) (*agentv1.VolumeOpResult, error) {
	ch, cleanup := waiter.Await(requestID)
	defer cleanup()

	if err := dispatcher.Send(serverID, cmd); err != nil {
		return nil, err
	}

	select {
	case result := <-ch:
		return result, nil
	case <-time.After(timeout):
		return nil, errOpTimeout
	}
}

// writeVolumeOpResult mirrors writeImageOpResult. A Docker-level failure
// (e.g. the in-use guard in internal/agent/docker/volumes.go's RemoveVolume
// refusing an in-use volume) is reported as success:false with a 200, not a
// transport error — same "docker-level failure isn't a transport error"
// convention writeImageOpResult already follows — so the Flutter client can
// distinguish "in use, retry with force" from a connectivity problem.
func writeVolumeOpResult(w http.ResponseWriter, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeOpWaiter, serverID, requestID string, cmd *agentv1.ControlMessage, timeout time.Duration, successStatus int) {
	result, err := sendAndAwaitVolumeOp(dispatcher, waiter, serverID, requestID, cmd, timeout)
	if err != nil {
		if errors.Is(err, deploy.ErrAgentNotConnected) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
		return
	}

	status := http.StatusOK
	if result.GetSuccess() {
		status = successStatus
	}
	writeJSON(w, status, volumeOpResponse{
		Success: result.GetSuccess(),
		Error:   result.GetErrorMessage(),
		Name:    result.GetName(),
	})
}

// handleCreateVolume creates a new named volume on serverID.
func handleCreateVolume(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeOpWaiter) http.HandlerFunc {
	type request struct {
		Name   string            `json:"name"`
		Driver string            `json:"driver,omitempty"`
		Labels map[string]string `json:"labels,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_CreateVolume{
				CreateVolume: &agentv1.CreateVolumeCommand{
					RequestId: requestID, ServerId: serverID, Name: req.Name, Driver: req.Driver, Labels: req.Labels,
				},
			},
		}
		writeVolumeOpResult(w, dispatcher, waiter, serverID, requestID, cmd, volumeOpTimeout, http.StatusCreated)
	}
}

// handleRemoveVolume removes one volume on serverID. ?force=true bypasses
// the agent's in-use guard (see RemoveVolume's doc comment in
// internal/agent/docker/volumes.go) — this is the server-side half of the
// UI's "block unless force" delete-protection guard; the client-side half
// is the type-to-confirm dialog before this request is ever sent.
func handleRemoveVolume(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeOpWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		name := r.PathValue("name")
		force := r.URL.Query().Get("force") == "true"

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_RemoveVolume{
				RemoveVolume: &agentv1.RemoveVolumeCommand{RequestId: requestID, ServerId: serverID, Name: name, Force: force},
			},
		}
		writeVolumeOpResult(w, dispatcher, waiter, serverID, requestID, cmd, volumeOpTimeout, http.StatusOK)
	}
}

// portConflictResponse is the JSON shape for a port-conflict check.
type portConflictResponse struct {
	Conflict    bool   `json:"conflict"`
	ContainerID string `json:"containerId,omitempty"`
}

// handleCheckPortConflict answers whether hostPort/protocol is already
// bound by another container on serverID, using the cached container
// inventory (store.FindPortConflict) — no agent round trip needed, see
// FindPortConflict's doc comment. Used both by a standalone "check before I
// submit" call from the port-mapping UI and, inline, by
// handleCreateContainer/handleRecreateContainer before dispatch.
func handleCheckPortConflict(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")

		hostPort, err := strconv.ParseUint(r.URL.Query().Get("hostPort"), 10, 16)
		if err != nil {
			http.Error(w, "hostPort is required and must be a valid port number", http.StatusBadRequest)
			return
		}
		protocol := r.URL.Query().Get("protocol")
		if protocol == "" {
			protocol = "tcp"
		}

		containerID, found, err := st.FindPortConflict(r.Context(), serverID, uint16(hostPort), protocol)
		if err != nil {
			log.Error("failed to check port conflict", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, portConflictResponse{Conflict: found, ContainerID: containerID})
	}
}
