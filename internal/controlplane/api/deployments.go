package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/compose"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

type deploymentStatusResponse struct {
	store.Deployment
	Events []store.DeploymentEvent `json:"events"`
}

// deploymentPreviewService summarizes one service compose-go resolved out
// of a compose file (after env-var substitution), for the "preview
// resources before deployment" step — what would be created, not whether
// the target server actually has room for it (that's pre-deployment
// validation, not implemented yet).
type deploymentPreviewService struct {
	Name             string   `json:"name"`
	Image            string   `json:"image"`
	Ports            []string `json:"ports,omitempty"`
	Volumes          []string `json:"volumes,omitempty"`
	EnvironmentCount int      `json:"environmentCount"`
}

type deploymentPreviewResponse struct {
	Name     string                     `json:"name"`
	Services []deploymentPreviewService `json:"services"`
}

// handlePreviewDeployment resolves a stack or compose file's services
// (same loader compose-go path composeImages uses for the image-policy
// check) without creating anything, so the deploy dialog can show exactly
// what would be created — images, ports, volumes — before the user
// confirms.
func handlePreviewDeployment(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		StackID       string            `json:"stackId"`
		ComposeFileID string            `json:"composeFileId"`
		Env           map[string]string `json:"env"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if (req.StackID == "") == (req.ComposeFileID == "") {
			http.Error(w, "exactly one of stackId/composeFileId is required", http.StatusBadRequest)
			return
		}

		env := req.Env
		if env == nil {
			env = map[string]string{}
		}

		var name, composeYAML string
		if req.StackID != "" {
			stack, err := st.GetStack(r.Context(), req.StackID)
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "stack not found", http.StatusNotFound)
				return
			}
			if err != nil {
				log.Error("failed to load stack", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			merged := map[string]string{}
			for k, v := range stack.DefaultEnv {
				merged[k] = v
			}
			for k, v := range env {
				merged[k] = v
			}
			env = merged
			name, composeYAML = stack.Name, stack.ComposeYAML
		} else {
			file, err := st.GetComposeFile(r.Context(), req.ComposeFileID)
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "compose file not found", http.StatusNotFound)
				return
			}
			if err != nil {
				log.Error("failed to load compose file", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			name, composeYAML = file.Name, file.Content
		}

		details := types.ConfigDetails{
			ConfigFiles: []types.ConfigFile{{Filename: "compose.yaml", Content: []byte(composeYAML)}},
			Environment: env,
		}
		project, err := loader.LoadWithContext(r.Context(), details, func(o *loader.Options) {
			o.SetProjectName("preview", true)
			o.SkipConsistencyCheck = true
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to parse compose file: " + err.Error()})
			return
		}

		services := make([]deploymentPreviewService, 0, len(project.Services))
		for svcName, svc := range project.Services {
			preview := deploymentPreviewService{
				Name:             svcName,
				Image:            svc.Image,
				EnvironmentCount: len(svc.Environment),
			}
			for _, p := range svc.Ports {
				protocol := p.Protocol
				if protocol == "" {
					protocol = "tcp"
				}
				if p.Published != "" {
					preview.Ports = append(preview.Ports, fmt.Sprintf("%s:%d/%s", p.Published, p.Target, protocol))
				} else {
					preview.Ports = append(preview.Ports, fmt.Sprintf("%d/%s", p.Target, protocol))
				}
			}
			for _, v := range svc.Volumes {
				entry := v.Source + ":" + v.Target
				if v.ReadOnly {
					entry += ":ro"
				}
				preview.Volumes = append(preview.Volumes, entry)
			}
			sort.Strings(preview.Ports)
			sort.Strings(preview.Volumes)
			services = append(services, preview)
		}
		sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

		writeJSON(w, http.StatusOK, deploymentPreviewResponse{Name: name, Services: services})
	}
}

func handleCreateDeployment(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus) http.HandlerFunc {
	type request struct {
		StackID       string            `json:"stackId"`
		ComposeFileID string            `json:"composeFileId"`
		ServerID      string            `json:"serverId"`
		Env           map[string]string `json:"env"`
		Environment   *string           `json:"environment,omitempty"`
		Tags          []string          `json:"tags,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.ServerID == "" || (req.StackID == "") == (req.ComposeFileID == "") {
			http.Error(w, "serverId and exactly one of stackId/composeFileId are required", http.StatusBadRequest)
			return
		}

		var (
			name         string
			composeYAML  string
			env          map[string]string
			deploymentID string
			err          error
		)

		if req.StackID != "" {
			stack, serr := st.GetStack(r.Context(), req.StackID)
			if errors.Is(serr, store.ErrNotFound) {
				http.Error(w, "stack not found", http.StatusNotFound)
				return
			}
			if serr != nil {
				log.Error("failed to load stack", "error", serr)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			env = map[string]string{}
			for k, v := range stack.DefaultEnv {
				env[k] = v
			}
			for k, v := range req.Env {
				env[k] = v
			}
			name, composeYAML = stack.Name, stack.ComposeYAML

			if images, ierr := composeImages(r.Context(), composeYAML, env); ierr == nil {
				if perr := enforceImagePolicy(r.Context(), st, images); perr != nil {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": perr.Error()})
					return
				}
			}

			deploymentID, err = st.CreateDeployment(r.Context(), stack.ID, req.ServerID, env, claims.UserID, req.Environment, req.Tags)
		} else {
			file, ferr := st.GetComposeFile(r.Context(), req.ComposeFileID)
			if errors.Is(ferr, store.ErrNotFound) {
				http.Error(w, "compose file not found", http.StatusNotFound)
				return
			}
			if ferr != nil {
				log.Error("failed to load compose file", "error", ferr)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			env = req.Env
			if env == nil {
				env = map[string]string{}
			}
			name, composeYAML = file.Name, file.Content

			if images, ierr := composeImages(r.Context(), composeYAML, env); ierr == nil {
				if perr := enforceImagePolicy(r.Context(), st, images); perr != nil {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": perr.Error()})
					return
				}
			}

			deploymentID, err = st.CreateComposeDeployment(r.Context(), file.ID, req.ServerID, env, claims.UserID, req.Environment, req.Tags)
		}
		if err != nil {
			log.Error("failed to create deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if event, err := st.AddDeploymentEvent(r.Context(), deploymentID, "pending", "deployment created"); err == nil {
			events.Publish(deploymentID, event)
		}

		if err := dispatchDeploy(r.Context(), log, st, dispatcher, events, deploymentID, req.ServerID, name, composeYAML, env); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{
				"deploymentId": deploymentID,
				"error":        "server not connected",
			})
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"deploymentId": deploymentID})
	}
}

func handleGetDeployment(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		deployment, err := st.GetDeployment(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "deployment not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		events, err := st.ListDeploymentEvents(r.Context(), id)
		if err != nil {
			log.Error("failed to load deployment events", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, deploymentStatusResponse{Deployment: *deployment, Events: events})
	}
}

// handleRedeployDeployment re-sends an existing deployment's compose stack
// to its server, keeping the same deployment_id. This is what actually
// exercises the agent's "redeploy = recreate" idempotency logic (internal/
// agent/docker.Deploy): a fresh POST /api/deployments always mints a new
// deployment_id, so it can never collide with a previous run's labeled
// containers — only re-sending the *same* deployment_id does that.
func handleRedeployDeployment(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		deployment, err := st.GetDeployment(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "deployment not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		name, composeYAML, err := st.ResolveDeploymentSource(r.Context(), deployment)
		if err != nil {
			log.Error("failed to resolve deployment source", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if images, err := composeImages(r.Context(), composeYAML, deployment.Env); err == nil {
			if err := enforceImagePolicy(r.Context(), st, images); err != nil {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
				return
			}
		}

		if event, err := st.AddDeploymentEvent(r.Context(), id, "pending", "redeploy requested"); err == nil {
			events.Publish(id, event)
		}

		if err := dispatchDeploy(r.Context(), log, st, dispatcher, events, id, deployment.ServerID, name, composeYAML, deployment.Env); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{
				"deploymentId": id,
				"error":        "server not connected",
			})
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"deploymentId": id})
	}
}

// handleRollbackDeployment is "Perform a controlled rollback": redeploys
// the whole stack using an earlier snapshot from the source compose
// file's version history (see compose_file_versions / UpdateComposeFile)
// instead of its current content — a one-time redeploy with historical
// YAML, not a change to the compose file itself, so it doesn't affect any
// other deployment sourced from the same file. Only meaningful for
// compose-file-sourced deployments, since the stacks catalog has no
// version history to roll back through.
func handleRollbackDeployment(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus) http.HandlerFunc {
	type request struct {
		VersionID string `json:"versionId"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		deployment, err := st.GetDeployment(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "deployment not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if deployment.ComposeFileID == nil {
			http.Error(w, "rollback is only supported for deployments sourced from a Compose file", http.StatusBadRequest)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.VersionID == "" {
			http.Error(w, "versionId is required", http.StatusBadRequest)
			return
		}

		version, err := st.GetComposeFileVersion(r.Context(), *deployment.ComposeFileID, req.VersionID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "version not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load compose file version", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if images, err := composeImages(r.Context(), version.Content, deployment.Env); err == nil {
			if err := enforceImagePolicy(r.Context(), st, images); err != nil {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
				return
			}
		}

		message := fmt.Sprintf("rolling back to version %d", version.VersionNumber)
		if event, err := st.AddDeploymentEvent(r.Context(), id, "pending", message); err == nil {
			events.Publish(id, event)
		}

		if err := dispatchDeploy(r.Context(), log, st, dispatcher, events, id, deployment.ServerID, version.Name, version.Content, deployment.Env); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{
				"deploymentId": id,
				"error":        "server not connected",
			})
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{
			"deploymentId":  id,
			"versionNumber": fmt.Sprint(version.VersionNumber),
		})
	}
}

// handleRedeployService redeploys a single named service within a
// deployment (pull its image, recreate just that service's container),
// leaving every other service running untouched — "Redeploy an
// individual service", the finer-grained counterpart to
// handleRedeployDeployment's whole-stack recreate.
func handleRedeployService(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter, events *deploy.EventBus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		serviceName := r.PathValue("service")

		deployment, err := st.GetDeployment(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "deployment not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		name, composeYAML, err := st.ResolveDeploymentSource(r.Context(), deployment)
		if err != nil {
			log.Error("failed to resolve deployment source", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		parsed := compose.Parse(composeYAML)
		found := false
		for _, s := range parsed.ServiceNames {
			if s == serviceName {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, fmt.Sprintf("service %q not found in this deployment's compose file", serviceName), http.StatusNotFound)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_DeployService{
				DeployService: &agentv1.DeployServiceCommand{
					RequestId:    requestID,
					ServerId:     deployment.ServerID,
					DeploymentId: deployment.ID,
					StackName:    name,
					ComposeYaml:  composeYAML,
					Env:          deployment.Env,
					ServiceName:  serviceName,
				},
			},
		}
		result, err := sendAndAwaitContainerOp(dispatcher, opWaiter, deployment.ServerID, requestID, cmd)
		if err != nil {
			if errors.Is(err, deploy.ErrAgentNotConnected) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
				return
			}
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
			return
		}

		message := fmt.Sprintf("service %q redeployed", serviceName)
		if !result.GetSuccess() {
			message = fmt.Sprintf("service %q redeploy failed: %s", serviceName, result.GetErrorMessage())
		}
		// The deployment's overall phase is left as-is — only one service
		// was touched, the rest of the stack kept running the whole time.
		if event, err := st.AddDeploymentEvent(r.Context(), id, deployment.Phase, message); err == nil {
			events.Publish(id, event)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"success": result.GetSuccess(),
			"error":   result.GetErrorMessage(),
		})
	}
}

// stackActionOutcomes maps a stack-level start/stop/restart action to the
// deployment phase and event message recorded once every container has
// been acted on. "remove" isn't here — handleDeploymentAction handles it
// separately, since removal also has to tear down the per-deployment
// network via a single UndeployCommand rather than fanning a per-container
// action out across whatever containers currently exist.
var stackActionOutcomes = map[string]struct{ phase, message string }{
	"start":   {phase: "running", message: "stack started"},
	"stop":    {phase: "stopped", message: "stack stopped"},
	"restart": {phase: "running", message: "stack restarted"},
}

// handleDeploymentAction applies a start/stop/restart/remove action to
// every container in a deployment — the stack-level counterpart to
// handleContainerAction/handleBulkContainerAction, which only ever knew
// about individually-named {server, container} pairs.
func handleDeploymentAction(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter, events *deploy.EventBus) http.HandlerFunc {
	type request struct {
		Action string `json:"action"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		deployment, err := st.GetDeployment(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "deployment not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if req.Action == "remove" {
			handleUndeployAction(w, r, log, st, dispatcher, opWaiter, events, deployment)
			return
		}

		outcome, ok := stackActionOutcomes[req.Action]
		if !ok {
			http.Error(w, "action must be one of start, stop, restart, remove", http.StatusBadRequest)
			return
		}
		action, err := parseContainerAction(req.Action)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		containers, err := st.ListContainersFiltered(r.Context(), store.ContainerFilter{DeploymentID: id})
		if err != nil {
			log.Error("failed to list deployment containers", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if len(containers) == 0 {
			http.Error(w, "deployment has no containers to act on", http.StatusNotFound)
			return
		}

		results := make([]bulkActionResult, len(containers))
		var wg sync.WaitGroup
		for i, c := range containers {
			wg.Add(1)
			go func(i int, c store.FleetContainer) {
				defer wg.Done()
				target := bulkActionTarget{ServerID: c.ServerID, ContainerID: c.ContainerID}
				results[i] = bulkDispatchOne(log, dispatcher, opWaiter, target, action, 0, false)
			}(i, c)
		}
		wg.Wait()

		allSucceeded := true
		for _, res := range results {
			if !res.Success {
				allSucceeded = false
				break
			}
		}
		message := outcome.message
		if !allSucceeded {
			message += " (some containers failed — see results)"
		}
		if err := st.UpdateDeploymentPhase(r.Context(), id, outcome.phase); err != nil {
			log.Error("failed to update deployment phase", "error", err)
		}
		if event, err := st.AddDeploymentEvent(r.Context(), id, outcome.phase, message); err == nil {
			events.Publish(id, event)
		}

		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}
}

// handleUndeployAction is handleDeploymentAction's "remove" branch: unlike
// start/stop/restart, removal also has to tear down the per-deployment
// network (see docker.Undeploy), so it dispatches a single UndeployCommand
// rather than fanning a per-container action out.
func handleUndeployAction(w http.ResponseWriter, r *http.Request, log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, opWaiter *deploy.OpWaiter, events *deploy.EventBus, deployment *store.Deployment) {
	requestID, ok := newRequestID(w, log)
	if !ok {
		return
	}

	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_Undeploy{
			Undeploy: &agentv1.UndeployCommand{
				RequestId:    requestID,
				ServerId:     deployment.ServerID,
				DeploymentId: deployment.ID,
			},
		},
	}
	result, err := sendAndAwaitContainerOp(dispatcher, opWaiter, deployment.ServerID, requestID, cmd)
	if err != nil {
		if errors.Is(err, deploy.ErrAgentNotConnected) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
		return
	}

	if result.GetSuccess() {
		if err := st.UpdateDeploymentPhase(r.Context(), deployment.ID, "removed"); err != nil {
			log.Error("failed to update deployment phase", "error", err)
		}
		if event, err := st.AddDeploymentEvent(r.Context(), deployment.ID, "removed", "stack removed"); err == nil {
			events.Publish(deployment.ID, event)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": result.GetSuccess(),
		"error":   result.GetErrorMessage(),
	})
}

// handleUpdateDeploymentMetadata sets a deployment's grouping metadata
// (environment/tags) after creation — owner (created_by) is fixed at
// creation time and not editable here.
func handleUpdateDeploymentMetadata(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Environment *string  `json:"environment,omitempty"`
		Tags        []string `json:"tags,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if err := st.UpdateDeploymentMetadata(r.Context(), id, req.Environment, req.Tags); err != nil {
			log.Error("failed to update deployment metadata", "deployment_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// composeImages parses composeYAML far enough to list every service's
// image, for enforceImagePolicy to check before a stack is dispatched. A
// parse failure here is deliberately swallowed by the caller (deploy still
// proceeds and the agent's own parseCompose reports the real error) — this
// is a policy pre-check, not compose validation.
func composeImages(ctx context.Context, composeYAML string, env map[string]string) ([]string, error) {
	details := types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{{Filename: "compose.yaml", Content: []byte(composeYAML)}},
		Environment: env,
	}
	project, err := loader.LoadWithContext(ctx, details, func(o *loader.Options) {
		o.SetProjectName("policy-check", true)
		o.SkipConsistencyCheck = true
	})
	if err != nil {
		return nil, err
	}

	images := make([]string, 0, len(project.Services))
	for _, svc := range project.Services {
		images = append(images, svc.Image)
	}
	return images, nil
}

// dispatchDeploy sends a DeployStackCommand to serverID and, if that fails
// (agent not currently connected), records and publishes a FAILED event.
func dispatchDeploy(ctx context.Context, log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus, deploymentID, serverID, stackName, composeYAML string, env map[string]string) error {
	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_DeployStack{
			DeployStack: &agentv1.DeployStackCommand{
				DeploymentId: deploymentID,
				StackName:    stackName,
				ComposeYaml:  composeYAML,
				Env:          env,
			},
		},
	}

	if err := dispatcher.Send(serverID, cmd); err != nil {
		log.Warn("failed to dispatch deploy command", "deployment_id", deploymentID, "server_id", serverID, "error", err)
		_ = st.UpdateDeploymentPhase(ctx, deploymentID, "failed")
		if event, addErr := st.AddDeploymentEvent(ctx, deploymentID, "failed", "server not connected: "+err.Error()); addErr == nil {
			events.Publish(deploymentID, event)
		}
		return err
	}
	return nil
}
