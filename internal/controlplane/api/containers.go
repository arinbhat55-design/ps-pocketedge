package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
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

// containerOpTimeout bounds how long a container lifecycle command
// (action/create/rename/clone/recreate/restart-policy) waits for the
// agent's ContainerOpResult reply. Longer than inspectTimeout since
// create/recreate may need to pull an image first.
const containerOpTimeout = 60 * time.Second

// handleListContainers serves the fleet-wide container inventory,
// filterable by name/image/server/status/owner/environment/tag. Grouping
// (by server/application/owner/environment/tags) is left to the client:
// every dimension is already present on each returned item.
func handleListContainers(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := store.ContainerFilter{
			Name:         q.Get("name"),
			Image:        q.Get("image"),
			ServerID:     q.Get("serverId"),
			Status:       q.Get("status"),
			OwnerID:      q.Get("ownerId"),
			Environment:  q.Get("environment"),
			Tags:         q["tag"],
			DeploymentID: q.Get("deploymentId"),
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
// demand from the agent. HealthLog/Command/Entrypoint/WorkingDir/Labels/
// Image back the troubleshooting view's health-check history and
// container-configuration inspect panel.
type containerDetailResponse struct {
	ContainerID                string            `json:"containerId"`
	Env                        []string          `json:"env"`
	RestartPolicyName          string            `json:"restartPolicyName"`
	RestartPolicyMaxRetryCount int               `json:"restartPolicyMaxRetryCount"`
	HealthStatus               string            `json:"healthStatus"`
	HealthFailingStreak        int               `json:"healthFailingStreak"`
	RestartCount               int               `json:"restartCount"`
	HealthLog                  []healthCheckJSON `json:"healthLog"`
	Command                    []string          `json:"command"`
	Entrypoint                 []string          `json:"entrypoint"`
	WorkingDir                 string            `json:"workingDir"`
	Labels                     map[string]string `json:"labels"`
	Image                      string            `json:"image"`
	NanoCPUs                   int64             `json:"nanoCpus"`
	MemoryLimitBytes           int64             `json:"memoryLimitBytes"`
	MemoryReservationBytes     int64             `json:"memoryReservationBytes"`
	PidsLimit                  int64             `json:"pidsLimit"`
}

// healthCheckJSON is one entry in a container's bounded health-check
// result history (Docker keeps its last 5 runs).
type healthCheckJSON struct {
	StartUnix int64  `json:"startUnix"`
	EndUnix   int64  `json:"endUnix"`
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output"`
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
			healthLog := make([]healthCheckJSON, len(detail.GetHealthLog()))
			for i, h := range detail.GetHealthLog() {
				healthLog[i] = healthCheckJSON{
					StartUnix: h.GetStartUnix(),
					EndUnix:   h.GetEndUnix(),
					ExitCode:  int(h.GetExitCode()),
					Output:    h.GetOutput(),
				}
			}
			writeJSON(w, http.StatusOK, containerDetailResponse{
				ContainerID:                detail.GetContainerId(),
				Env:                        detail.GetEnv(),
				RestartPolicyName:          detail.GetRestartPolicyName(),
				RestartPolicyMaxRetryCount: int(detail.GetRestartPolicyMaxRetryCount()),
				HealthStatus:               detail.GetHealthStatus(),
				HealthFailingStreak:        int(detail.GetHealthFailingStreak()),
				RestartCount:               int(detail.GetRestartCount()),
				HealthLog:                  healthLog,
				Command:                    detail.GetCommand(),
				Entrypoint:                 detail.GetEntrypoint(),
				WorkingDir:                 detail.GetWorkingDir(),
				Labels:                     detail.GetLabels(),
				Image:                      detail.GetImage(),
				NanoCPUs:                   detail.GetNanoCpus(),
				MemoryLimitBytes:           detail.GetMemoryLimitBytes(),
				MemoryReservationBytes:     detail.GetMemoryReservationBytes(),
				PidsLimit:                  detail.GetPidsLimit(),
			})
		case <-time.After(inspectTimeout):
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for agent response"})
		case <-r.Context().Done():
		}
	}
}

// errOpTimeout is sendAndAwaitContainerOp's error when no ContainerOpResult
// arrives within containerOpTimeout.
var errOpTimeout = errors.New("timed out waiting for agent response")

// sendAndAwaitContainerOp dispatches cmd to serverID and blocks up to
// containerOpTimeout for the agent's ContainerOpResult reply — the same
// dispatch+correlate+timeout pattern handleInspectContainer uses above.
// The returned error is only ever deploy.ErrAgentNotConnected or
// errOpTimeout; an application-level failure (e.g. "no such image") comes
// back inside a successfully-received *ContainerOpResult, not as an error
// here.
func sendAndAwaitContainerOp(dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter, serverID, requestID string, cmd *agentv1.ControlMessage) (*agentv1.ContainerOpResult, error) {
	ch, cleanup := opWaiter.Await(requestID)
	defer cleanup()

	if err := dispatcher.Send(serverID, cmd); err != nil {
		return nil, err
	}

	select {
	case result := <-ch:
		return result, nil
	case <-time.After(containerOpTimeout):
		return nil, errOpTimeout
	}
}

// containerOpResponse is the JSON shape returned for a single container
// lifecycle command that completed its round trip to the agent (whether
// the operation itself succeeded or failed — a docker-level failure like
// "container already stopped" isn't a transport error, so it's reported
// here with success:false rather than a non-2xx status).
type containerOpResponse struct {
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
	ContainerID string `json:"containerId,omitempty"`
}

// writeContainerOpResult sends cmd and writes containerOpResponse once a
// reply arrives, or a 409/504 if the command never reached/answered from
// the agent at all. successStatus is used only when the operation itself
// succeeded (e.g. 201 for create/clone, 200 otherwise).
func writeContainerOpResult(w http.ResponseWriter, log *slog.Logger, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter, serverID, requestID string, cmd *agentv1.ControlMessage, successStatus int) {
	result, err := sendAndAwaitContainerOp(dispatcher, opWaiter, serverID, requestID, cmd)
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
	writeJSON(w, status, containerOpResponse{
		Success:     result.GetSuccess(),
		Error:       result.GetErrorMessage(),
		ContainerID: result.GetContainerId(),
	})
}

// newRequestID is auth.RandomToken with the internal-error response
// every container-op handler below needs on failure, factored out so each
// handler is one call instead of a repeated 4-line error branch.
func newRequestID(w http.ResponseWriter, log *slog.Logger) (string, bool) {
	requestID, err := auth.RandomToken()
	if err != nil {
		log.Error("failed to generate request id", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return "", false
	}
	return requestID, true
}

func parseContainerAction(s string) (agentv1.ContainerAction, error) {
	switch s {
	case "start":
		return agentv1.ContainerAction_CONTAINER_ACTION_START, nil
	case "stop":
		return agentv1.ContainerAction_CONTAINER_ACTION_STOP, nil
	case "restart":
		return agentv1.ContainerAction_CONTAINER_ACTION_RESTART, nil
	case "pause":
		return agentv1.ContainerAction_CONTAINER_ACTION_PAUSE, nil
	case "resume":
		return agentv1.ContainerAction_CONTAINER_ACTION_RESUME, nil
	case "kill":
		return agentv1.ContainerAction_CONTAINER_ACTION_KILL, nil
	case "remove":
		return agentv1.ContainerAction_CONTAINER_ACTION_REMOVE, nil
	default:
		return agentv1.ContainerAction_CONTAINER_ACTION_UNSPECIFIED, fmt.Errorf("unknown action %q", s)
	}
}

// handleContainerAction applies one lifecycle action (start/stop/restart/
// pause/resume/kill/remove) to a single container.
func handleContainerAction(log *slog.Logger, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter) http.HandlerFunc {
	type request struct {
		Action         string `json:"action"`
		TimeoutSeconds int32  `json:"timeoutSeconds,omitempty"`
		Force          bool   `json:"force,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		action, err := parseContainerAction(req.Action)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_ContainerAction{
				ContainerAction: &agentv1.ContainerActionCommand{
					RequestId:      requestID,
					ServerId:       serverID,
					ContainerId:    containerID,
					Action:         action,
					TimeoutSeconds: req.TimeoutSeconds,
					Force:          req.Force,
				},
			},
		}
		writeContainerOpResult(w, log, dispatcher, opWaiter, serverID, requestID, cmd, http.StatusOK)
	}
}

// bulkActionTarget is one {server, container} pair in a bulk-action
// request.
type bulkActionTarget struct {
	ServerID    string `json:"serverId"`
	ContainerID string `json:"containerId"`
}

// bulkActionResult reports one target's outcome — partial failure across a
// bulk request isn't an error response, it's data the client renders as a
// per-item success/failure summary.
type bulkActionResult struct {
	ServerID    string `json:"serverId"`
	ContainerID string `json:"containerId"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
}

// handleBulkContainerAction applies one action to many containers,
// possibly spanning multiple servers, fanning out one dispatch+await per
// target concurrently.
func handleBulkContainerAction(log *slog.Logger, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter) http.HandlerFunc {
	type request struct {
		Targets        []bulkActionTarget `json:"targets"`
		Action         string             `json:"action"`
		TimeoutSeconds int32              `json:"timeoutSeconds,omitempty"`
		Force          bool               `json:"force,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		action, err := parseContainerAction(req.Action)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.Targets) == 0 {
			http.Error(w, "targets is required", http.StatusBadRequest)
			return
		}

		results := make([]bulkActionResult, len(req.Targets))
		var wg sync.WaitGroup
		for i, target := range req.Targets {
			wg.Add(1)
			go func(i int, target bulkActionTarget) {
				defer wg.Done()
				results[i] = bulkDispatchOne(log, dispatcher, opWaiter, target, action, req.TimeoutSeconds, req.Force)
			}(i, target)
		}
		wg.Wait()

		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}
}

func bulkDispatchOne(log *slog.Logger, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter, target bulkActionTarget, action agentv1.ContainerAction, timeoutSeconds int32, force bool) bulkActionResult {
	requestID, err := auth.RandomToken()
	if err != nil {
		log.Error("failed to generate request id", "error", err)
		return bulkActionResult{ServerID: target.ServerID, ContainerID: target.ContainerID, Error: "internal error"}
	}

	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_ContainerAction{
			ContainerAction: &agentv1.ContainerActionCommand{
				RequestId:      requestID,
				ServerId:       target.ServerID,
				ContainerId:    target.ContainerID,
				Action:         action,
				TimeoutSeconds: timeoutSeconds,
				Force:          force,
			},
		},
	}
	result, err := sendAndAwaitContainerOp(dispatcher, opWaiter, target.ServerID, requestID, cmd)
	if err != nil {
		return bulkActionResult{ServerID: target.ServerID, ContainerID: target.ContainerID, Error: err.Error()}
	}
	return bulkActionResult{
		ServerID:    target.ServerID,
		ContainerID: target.ContainerID,
		Success:     result.GetSuccess(),
		Error:       result.GetErrorMessage(),
	}
}

// containerConfigRequest is the JSON shape for the desired configuration
// of a standalone container, used by both handleCreateContainer and
// handleRecreateContainer.
type containerConfigRequest struct {
	Image                      string                    `json:"image"`
	Name                       string                    `json:"name"`
	Command                    []string                  `json:"command,omitempty"`
	Env                        []string                  `json:"env,omitempty"`
	Ports                      []containerPortSpecJSON   `json:"ports,omitempty"`
	Volumes                    []containerVolumeSpecJSON `json:"volumes,omitempty"`
	RestartPolicyName          string                    `json:"restartPolicyName,omitempty"`
	RestartPolicyMaxRetryCount int32                     `json:"restartPolicyMaxRetryCount,omitempty"`
	Labels                     map[string]string         `json:"labels,omitempty"`
	// Resource limits — 0/omitted means "not set" (unlimited).
	NanoCPUs               int64 `json:"nanoCpus,omitempty"`
	MemoryLimitBytes       int64 `json:"memoryLimitBytes,omitempty"`
	MemoryReservationBytes int64 `json:"memoryReservationBytes,omitempty"`
	PidsLimit              int64 `json:"pidsLimit,omitempty"`
}

type containerPortSpecJSON struct {
	ContainerPort uint32 `json:"containerPort"`
	HostPort      uint32 `json:"hostPort,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
}

type containerVolumeSpecJSON struct {
	VolumeName string `json:"volumeName"`
	Target     string `json:"target"`
	ReadOnly   bool   `json:"readOnly,omitempty"`
}

func (req containerConfigRequest) toProto() *agentv1.ContainerConfig {
	ports := make([]*agentv1.ContainerPortSpec, 0, len(req.Ports))
	for _, p := range req.Ports {
		ports = append(ports, &agentv1.ContainerPortSpec{
			ContainerPort: p.ContainerPort,
			HostPort:      p.HostPort,
			Protocol:      p.Protocol,
		})
	}
	volumes := make([]*agentv1.ContainerVolumeSpec, 0, len(req.Volumes))
	for _, v := range req.Volumes {
		volumes = append(volumes, &agentv1.ContainerVolumeSpec{
			VolumeName: v.VolumeName,
			Target:     v.Target,
			ReadOnly:   v.ReadOnly,
		})
	}
	return &agentv1.ContainerConfig{
		Image:                      req.Image,
		Name:                       req.Name,
		Command:                    req.Command,
		Env:                        req.Env,
		Ports:                      ports,
		Volumes:                    volumes,
		RestartPolicyName:          req.RestartPolicyName,
		RestartPolicyMaxRetryCount: req.RestartPolicyMaxRetryCount,
		Labels:                     req.Labels,
		NanoCpus:                   req.NanoCPUs,
		MemoryLimitBytes:           req.MemoryLimitBytes,
		MemoryReservationBytes:     req.MemoryReservationBytes,
		PidsLimit:                  req.PidsLimit,
	}
}

// checkPortConflicts looks up each requested host port against
// store.FindPortConflict (the same cached-container-inventory check
// handleCheckPortConflict, volumes.go, exposes standalone), so create/
// recreate get the guard for free without the client needing a separate
// round trip first. excludeContainerID lets handleRecreateContainer ignore
// a match against the very container being recreated, which is about to
// give up that port itself. A HostPort of 0 (agent-assigned) never
// conflicts.
func checkPortConflicts(ctx context.Context, st *store.Store, serverID, excludeContainerID string, ports []containerPortSpecJSON) (message string, conflict bool, err error) {
	for _, p := range ports {
		if p.HostPort == 0 {
			continue
		}
		protocol := p.Protocol
		if protocol == "" {
			protocol = "tcp"
		}
		containerID, found, err := st.FindPortConflict(ctx, serverID, uint16(p.HostPort), protocol)
		if err != nil {
			return "", false, err
		}
		if found && containerID != excludeContainerID {
			return fmt.Sprintf("host port %d/%s already used by container %s", p.HostPort, protocol, containerID), true, nil
		}
	}
	return "", false, nil
}

// handleCreateContainer creates and starts a new standalone container
// (not part of a deployed stack) on serverID from the posted config.
func handleCreateContainer(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")

		var req containerConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Image == "" {
			http.Error(w, "image is required", http.StatusBadRequest)
			return
		}
		if err := enforceImagePolicy(r.Context(), st, []string{req.Image}); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		if msg, conflict, err := checkPortConflicts(r.Context(), st, serverID, "", req.Ports); err != nil {
			log.Error("failed to check port conflicts", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		} else if conflict {
			writeJSON(w, http.StatusConflict, map[string]string{"error": msg})
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_CreateContainer{
				CreateContainer: &agentv1.CreateContainerCommand{
					RequestId: requestID,
					ServerId:  serverID,
					Config:    req.toProto(),
				},
			},
		}
		writeContainerOpResult(w, log, dispatcher, opWaiter, serverID, requestID, cmd, http.StatusCreated)
	}
}

// handleRenameContainer renames an existing container.
func handleRenameContainer(log *slog.Logger, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter) http.HandlerFunc {
	type request struct {
		NewName string `json:"newName"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewName == "" {
			http.Error(w, "newName is required", http.StatusBadRequest)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_RenameContainer{
				RenameContainer: &agentv1.RenameContainerCommand{
					RequestId:   requestID,
					ServerId:    serverID,
					ContainerId: containerID,
					NewName:     req.NewName,
				},
			},
		}
		writeContainerOpResult(w, log, dispatcher, opWaiter, serverID, requestID, cmd, http.StatusOK)
	}
}

// handleCloneContainer duplicates an existing container's configuration
// into a new, not-started container.
func handleCloneContainer(log *slog.Logger, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter) http.HandlerFunc {
	type request struct {
		NewName string `json:"newName"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewName == "" {
			http.Error(w, "newName is required", http.StatusBadRequest)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_CloneContainer{
				CloneContainer: &agentv1.CloneContainerCommand{
					RequestId:   requestID,
					ServerId:    serverID,
					ContainerId: containerID,
					NewName:     req.NewName,
				},
			},
		}
		writeContainerOpResult(w, log, dispatcher, opWaiter, serverID, requestID, cmd, http.StatusCreated)
	}
}

// handleRecreateContainer stops and removes an existing container and
// creates a fresh one in its place from the posted (edited) config. When
// the image is changing, the container's current image is captured into
// image_rollback_history beforehand — that's what makes "rollback to
// previous image" (rollback.go) possible after this call.
func handleRecreateContainer(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		var req containerConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Image == "" {
			http.Error(w, "image is required", http.StatusBadRequest)
			return
		}
		if err := enforceImagePolicy(r.Context(), st, []string{req.Image}); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		if msg, conflict, err := checkPortConflicts(r.Context(), st, serverID, containerID, req.Ports); err != nil {
			log.Error("failed to check port conflicts", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		} else if conflict {
			writeJSON(w, http.StatusConflict, map[string]string{"error": msg})
			return
		}

		previousImage, _ := findContainerState(r.Context(), st, serverID, containerID)

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
					Config:      req.toProto(),
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
		if result.GetSuccess() && previousImage != nil && previousImage.Image != "" && previousImage.Image != req.Image {
			if err := st.AddImageRollbackHistory(r.Context(), serverID, containerID, previousImage.Image, rollbackHistoryLimit); err != nil {
				log.Warn("failed to record image rollback history", "server_id", serverID, "container_id", containerID, "error", err)
			}
		}

		writeJSON(w, http.StatusOK, containerOpResponse{
			Success:     result.GetSuccess(),
			Error:       result.GetErrorMessage(),
			ContainerID: result.GetContainerId(),
		})
	}
}

// handleUpdateRestartPolicy changes an existing container's restart policy
// live, without recreating it.
func handleUpdateRestartPolicy(log *slog.Logger, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter) http.HandlerFunc {
	type request struct {
		RestartPolicyName          string `json:"restartPolicyName"`
		RestartPolicyMaxRetryCount int32  `json:"restartPolicyMaxRetryCount"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_UpdateRestartPolicy{
				UpdateRestartPolicy: &agentv1.UpdateRestartPolicyCommand{
					RequestId:                  requestID,
					ServerId:                   serverID,
					ContainerId:                containerID,
					RestartPolicyName:          req.RestartPolicyName,
					RestartPolicyMaxRetryCount: req.RestartPolicyMaxRetryCount,
				},
			},
		}
		writeContainerOpResult(w, log, dispatcher, opWaiter, serverID, requestID, cmd, http.StatusOK)
	}
}

// handleUpdateResourceLimits changes an existing container's CPU/memory/
// process limits live, without recreating it — same shape as
// handleUpdateRestartPolicy above.
func handleUpdateResourceLimits(log *slog.Logger, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter) http.HandlerFunc {
	type request struct {
		NanoCPUs               int64 `json:"nanoCpus"`
		MemoryLimitBytes       int64 `json:"memoryLimitBytes"`
		MemoryReservationBytes int64 `json:"memoryReservationBytes"`
		PidsLimit              int64 `json:"pidsLimit"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_UpdateResourceLimits{
				UpdateResourceLimits: &agentv1.UpdateResourceLimitsCommand{
					RequestId:              requestID,
					ServerId:               serverID,
					ContainerId:            containerID,
					NanoCpus:               req.NanoCPUs,
					MemoryLimitBytes:       req.MemoryLimitBytes,
					MemoryReservationBytes: req.MemoryReservationBytes,
					PidsLimit:              req.PidsLimit,
				},
			},
		}
		writeContainerOpResult(w, log, dispatcher, opWaiter, serverID, requestID, cmd, http.StatusOK)
	}
}
