package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// inspectTimeout bounds how long handleInspectContainer waits for the
// agent's ContainerDetail reply — generous for a single Docker Engine API
// round-trip relayed over an already-open gRPC stream, but short enough
// that a hung/unreachable agent doesn't tie up the request indefinitely.
const inspectTimeout = 10 * time.Second

// handleListContainers serves the fleet-wide container inventory,
// filterable by name/image/server/status/owner/environment/tag. Grouping
// (by server/application/owner/environment/tags) is left to the client:
// every dimension is already present on each returned item.
func handleListContainers(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := store.ContainerFilter{
			Name:        q.Get("name"),
			Image:       q.Get("image"),
			ServerID:    q.Get("serverId"),
			Status:      q.Get("status"),
			OwnerID:     q.Get("ownerId"),
			Environment: q.Get("environment"),
			Tags:        q["tag"],
		}

		containers, err := st.ListContainersFiltered(r.Context(), filter)
		if err != nil {
			log.Error("failed to list containers", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, containers)
	}
}

// containerDetailResponse is the JSON shape for a successful inspect
// response — the expensive fields InspectContainerCommand fetched on
// demand from the agent.
type containerDetailResponse struct {
	ContainerID                string   `json:"containerId"`
	Env                        []string `json:"env"`
	RestartPolicyName          string   `json:"restartPolicyName"`
	RestartPolicyMaxRetryCount int      `json:"restartPolicyMaxRetryCount"`
	HealthStatus               string   `json:"healthStatus"`
	HealthFailingStreak        int      `json:"healthFailingStreak"`
	RestartCount               int      `json:"restartCount"`
}

// handleInspectContainer dispatches an InspectContainerCommand to the
// target agent and blocks up to inspectTimeout for its ContainerDetail
// reply, correlated via waiter. Synchronous HTTP response — no websocket
// needed for a single request/reply expected to complete in well under a
// second.
func handleInspectContainer(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.InspectWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		requestID, err := auth.RandomToken()
		if err != nil {
			log.Error("failed to generate inspect request id", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		ch, cleanup := waiter.Await(requestID)
		defer cleanup()

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_InspectContainer{
				InspectContainer: &agentv1.InspectContainerCommand{
					RequestId:   requestID,
					ServerId:    serverID,
					ContainerId: containerID,
				},
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
			writeJSON(w, http.StatusOK, containerDetailResponse{
				ContainerID:                detail.GetContainerId(),
				Env:                        detail.GetEnv(),
				RestartPolicyName:          detail.GetRestartPolicyName(),
				RestartPolicyMaxRetryCount: int(detail.GetRestartPolicyMaxRetryCount()),
				HealthStatus:               detail.GetHealthStatus(),
				HealthFailingStreak:        int(detail.GetHealthFailingStreak()),
				RestartCount:               int(detail.GetRestartCount()),
			})
		case <-time.After(inspectTimeout):
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for agent response"})
		case <-r.Context().Done():
		}
	}
}
