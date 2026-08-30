package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

type deploymentStatusResponse struct {
	store.Deployment
	Events []store.DeploymentEvent `json:"events"`
}

func handleCreateDeployment(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus) http.HandlerFunc {
	type request struct {
		StackID     string            `json:"stackId"`
		ServerID    string            `json:"serverId"`
		Env         map[string]string `json:"env"`
		Environment *string           `json:"environment,omitempty"`
		Tags        []string          `json:"tags,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.StackID == "" || req.ServerID == "" {
			http.Error(w, "stackId and serverId are required", http.StatusBadRequest)
			return
		}

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

		env := map[string]string{}
		for k, v := range stack.DefaultEnv {
			env[k] = v
		}
		for k, v := range req.Env {
			env[k] = v
		}

		deploymentID, err := st.CreateDeployment(r.Context(), stack.ID, req.ServerID, env, claims.UserID, req.Environment, req.Tags)
		if err != nil {
			log.Error("failed to create deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if event, err := st.AddDeploymentEvent(r.Context(), deploymentID, "pending", "deployment created"); err == nil {
			events.Publish(deploymentID, event)
		}

		if err := dispatchDeploy(r.Context(), log, st, dispatcher, events, deploymentID, req.ServerID, stack.Name, stack.ComposeYAML, env); err != nil {
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

		stack, err := st.GetStack(r.Context(), deployment.StackID)
		if err != nil {
			log.Error("failed to load stack", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if event, err := st.AddDeploymentEvent(r.Context(), id, "pending", "redeploy requested"); err == nil {
			events.Publish(id, event)
		}

		if err := dispatchDeploy(r.Context(), log, st, dispatcher, events, id, deployment.ServerID, stack.Name, stack.ComposeYAML, deployment.Env); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{
				"deploymentId": id,
				"error":        "server not connected",
			})
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"deploymentId": id})
	}
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
