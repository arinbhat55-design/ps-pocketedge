package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// rollbackHistoryLimit bounds how many previous images image_rollback_history
// keeps per container — this exists to support "undo the last image
// change", not a full audit trail.
const rollbackHistoryLimit = 5

// handleListImageRollbackHistory returns a container's recorded previous
// images, most recent first, so the UI can show whether a rollback is
// available at all.
func handleListImageRollbackHistory(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		history, err := st.ListImageRollbackHistory(r.Context(), serverID, containerID)
		if err != nil {
			log.Error("failed to load image rollback history", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, history)
	}
}

// handleRollbackContainer recreates a standalone container on its most
// recently recorded previous image, keeping every other setting (name,
// ports, volumes, restart policy, resource limits) unchanged and refreshing env from a live
// inspect. It assembles a full ContainerConfig from the container's
// current known state — the server_containers row (name/ports/mounts)
// plus a fresh InspectContainerCommand (env/restart policy) — combined
// server-side so the caller only needs to POST once.
func handleRollbackContainer(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, inspectWaiter *deploy.InspectWaiter, opWaiter *deploy.OpWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		entry, err := st.LatestImageRollbackHistory(r.Context(), serverID, containerID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "no rollback history for this container", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load rollback history", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		current, err := awaitContainerState(r.Context(), st, serverID, containerID)
		if err != nil {
			http.Error(w, "container not found", http.StatusNotFound)
			return
		}

		detail, err := awaitInspect(dispatcher, inspectWaiter, serverID, containerID)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if !detail.GetFound() {
			http.Error(w, "container not found on agent", http.StatusNotFound)
			return
		}

		config := containerConfigFromState(current, detail, entry.PreviousImage)

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}
		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_RecreateContainer{
				RecreateContainer: &agentv1.RecreateContainerCommand{
					RequestId:   requestID,
					ServerId:    serverID,
					ContainerId: containerID,
					Config:      config,
				},
			},
		}

		result, err := sendAndAwaitContainerOp(dispatcher, opWaiter, serverID, requestID, cmd)
		if err != nil {
			if errors.Is(err, deploy.ErrAgentNotConnected) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
				return
			}
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
			return
		}
		if result.GetSuccess() {
			// The entry is consumed, the rest of the history follows the
			// container to its new ID, and the image we just rolled away
			// from becomes rollback-able again, same as any other recreate
			// that changes the image (see handleRecreateContainer).
			if err := st.RecordContainerRecreate(r.Context(), serverID, containerID, result.GetContainerId(), entry.ID, current.Image, rollbackHistoryLimit); err != nil {
				log.Warn("failed to update image rollback history", "server_id", serverID, "container_id", containerID, "error", err)
			}
		}

		writeJSON(w, http.StatusOK, containerOpResponse{
			Success:     result.GetSuccess(),
			Error:       result.GetErrorMessage(),
			ContainerID: result.GetContainerId(),
		})
	}
}

// findContainerState looks up containerID's last-known state from
// server_containers (name/ports/mounts/image), used to assemble a
// ContainerConfig without requiring the caller to already have it.
func findContainerState(ctx context.Context, st *store.Store, serverID, containerID string) (*store.ContainerState, error) {
	states, err := st.ListContainers(ctx, serverID)
	if err != nil {
		return nil, err
	}
	for _, c := range states {
		if c.ContainerID == containerID {
			return &c, nil
		}
	}
	return nil, errors.New("container not found")
}

// containerStateWait bounds how long awaitContainerState waits for a
// container to show up in server_containers — one heartbeat interval plus
// slack, since a container recreated moments ago is only reported on the
// agent's next heartbeat.
const containerStateWait = 25 * time.Second

// awaitContainerState is findContainerState, polling until the container
// appears or containerStateWait passes.
func awaitContainerState(ctx context.Context, st *store.Store, serverID, containerID string) (*store.ContainerState, error) {
	deadline := time.Now().Add(containerStateWait)
	for {
		state, err := findContainerState(ctx, st, serverID, containerID)
		if err == nil || time.Now().After(deadline) {
			return state, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// awaitInspect runs the same dispatch+correlate+timeout as
// handleInspectContainer (containers.go), factored out so rollback can
// reuse it.
func awaitInspect(dispatcher *deploy.Dispatcher, waiter *deploy.InspectWaiter, serverID, containerID string) (*agentv1.ContainerDetail, error) {
	requestID, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}

	ch, cleanup := waiter.Await(requestID)
	defer cleanup()

	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_InspectContainer{
			InspectContainer: &agentv1.InspectContainerCommand{RequestId: requestID, ServerId: serverID, ContainerId: containerID},
		},
	}
	if err := dispatcher.Send(serverID, cmd); err != nil {
		return nil, err
	}

	select {
	case detail := <-ch:
		return detail, nil
	case <-time.After(inspectTimeout):
		return nil, errOpTimeout
	}
}

// containerConfigFromState combines a container's known state and live
// inspect detail into a full ContainerConfig with its image swapped to
// image — everything RecreateContainerCommand needs to recreate the
// container identically except for the image.
func containerConfigFromState(state *store.ContainerState, detail *agentv1.ContainerDetail, image string) *agentv1.ContainerConfig {
	ports := make([]*agentv1.ContainerPortSpec, 0, len(state.Ports))
	// Docker reports a published port once per address family (0.0.0.0
	// and ::); binding both entries again would publish the port twice.
	type binding struct {
		private, public uint16
		protocol        string
	}
	seen := map[binding]bool{}
	for _, p := range state.Ports {
		if p.PublicPort == 0 {
			continue
		}
		b := binding{p.PrivatePort, p.PublicPort, p.Type}
		if seen[b] {
			continue
		}
		seen[b] = true
		ports = append(ports, &agentv1.ContainerPortSpec{
			ContainerPort: uint32(p.PrivatePort),
			HostPort:      uint32(p.PublicPort),
			Protocol:      p.Type,
		})
	}

	volumes := make([]*agentv1.ContainerVolumeSpec, 0, len(state.Mounts))
	for _, m := range state.Mounts {
		if m.Type != "volume" || m.Name == "" {
			continue
		}
		volumes = append(volumes, &agentv1.ContainerVolumeSpec{
			VolumeName: m.Name,
			Target:     m.Destination,
			ReadOnly:   !m.ReadWrite,
		})
	}

	return &agentv1.ContainerConfig{
		Image:                      image,
		Name:                       state.Name,
		Env:                        detail.GetEnv(),
		Ports:                      ports,
		Volumes:                    volumes,
		RestartPolicyName:          detail.GetRestartPolicyName(),
		RestartPolicyMaxRetryCount: detail.GetRestartPolicyMaxRetryCount(),
		NanoCpus:                   detail.GetNanoCpus(),
		MemoryLimitBytes:           detail.GetMemoryLimitBytes(),
		MemoryReservationBytes:     detail.GetMemoryReservationBytes(),
		PidsLimit:                  detail.GetPidsLimit(),
	}
}
