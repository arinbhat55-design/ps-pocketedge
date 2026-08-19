package api

import (
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
		StackID  string            `json:"stackId"`
		ServerID string            `json:"serverId"`
		Env      map[string]string `json:"env"`
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

		deploymentID, err := st.CreateDeployment(r.Context(), stack.ID, req.ServerID, env, claims.UserID)
		if err != nil {
			log.Error("failed to create deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if event, err := st.AddDeploymentEvent(r.Context(), deploymentID, "pending", "deployment created"); err == nil {
			events.Publish(deploymentID, event)
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_DeployStack{
				DeployStack: &agentv1.DeployStackCommand{
					DeploymentId: deploymentID,
					StackName:    stack.Name,
					ComposeYaml:  stack.ComposeYAML,
					Env:          env,
				},
			},
		}

		if err := dispatcher.Send(req.ServerID, cmd); err != nil {
			log.Warn("failed to dispatch deploy command", "deployment_id", deploymentID, "server_id", req.ServerID, "error", err)
			_ = st.UpdateDeploymentPhase(r.Context(), deploymentID, "failed")
			if event, err := st.AddDeploymentEvent(r.Context(), deploymentID, "failed", "server not connected: "+err.Error()); err == nil {
				events.Publish(deploymentID, event)
			}
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
