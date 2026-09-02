package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// handleListEnvVarGroups lists env var groups, optionally narrowed to one
// environment via ?environment=.
func handleListEnvVarGroups(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groups, err := st.ListEnvVarGroups(r.Context(), r.URL.Query().Get("environment"))
		if err != nil {
			log.Error("failed to list env var groups", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, groups)
	}
}

func handleGetEnvVarGroup(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g, err := st.GetEnvVarGroup(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "env var group not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load env var group", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, g)
	}
}

type envVarGroupRequest struct {
	Name        string            `json:"name"`
	Environment string            `json:"environment"`
	Variables   []store.Parameter `json:"variables"`
}

// validate trims and checks name/environment, defaulting Environment to
// "development" when empty rather than rejecting the request — most
// callers building a quick ad-hoc group won't bother picking one.
func (req *envVarGroupRequest) validate() error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return errors.New("name is required")
	}
	if req.Environment == "" {
		req.Environment = "development"
	}
	if !store.IsValidEnvironment(req.Environment) {
		return errors.New("environment must be one of development, test, staging, production")
	}
	for i, v := range req.Variables {
		if strings.TrimSpace(v.Key) == "" {
			return errors.New("every variable needs a key")
		}
		req.Variables[i].Key = strings.TrimSpace(v.Key)
	}
	return nil
}

func handleCreateEnvVarGroup(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req envVarGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := req.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var createdBy string
		if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
			createdBy = claims.UserID
		}

		id, err := st.CreateEnvVarGroup(r.Context(), req.Name, req.Environment, req.Variables, createdBy)
		if err != nil {
			log.Error("failed to create env var group", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		g, err := st.GetEnvVarGroup(r.Context(), id)
		if err != nil {
			log.Error("failed to reload created env var group", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, g)
	}
}

func handleUpdateEnvVarGroup(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		var req envVarGroupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := req.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := st.UpdateEnvVarGroup(r.Context(), id, req.Name, req.Environment, req.Variables); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "env var group not found", http.StatusNotFound)
				return
			}
			log.Error("failed to update env var group", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		g, err := st.GetEnvVarGroup(r.Context(), id)
		if err != nil {
			log.Error("failed to reload updated env var group", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, g)
	}
}

func handleDeleteEnvVarGroup(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := st.DeleteEnvVarGroup(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "env var group not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to delete env var group", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
