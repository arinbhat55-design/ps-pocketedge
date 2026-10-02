package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

func handleListApprovedImages(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		patterns, err := st.ListApprovedImages(r.Context())
		if err != nil {
			log.Error("failed to list approved images", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, patterns)
	}
}

func handleCreateApprovedImage(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Pattern string `json:"pattern"`
		Note    string `json:"note,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Pattern) == "" {
			http.Error(w, "pattern is required", http.StatusBadRequest)
			return
		}

		req.Pattern = strings.TrimSpace(req.Pattern)
		if err := store.ValidateImagePattern(req.Pattern); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		id, err := st.CreateApprovedImage(r.Context(), req.Pattern, req.Note, claims.UserID)
		if err != nil {
			log.Error("failed to create approved image pattern", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

func handleDeleteApprovedImage(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := st.DeleteApprovedImage(r.Context(), id); err != nil {
			log.Error("failed to delete approved image pattern", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleGetImagePolicySettings(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		enabled, err := st.GetImagePolicyEnabled(r.Context())
		if err != nil {
			log.Error("failed to load image policy settings", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
	}
}

func handleUpdateImagePolicySettings(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Enabled bool `json:"enabled"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := st.SetImagePolicyEnabled(r.Context(), req.Enabled); err != nil {
			log.Error("failed to update image policy settings", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// errImageNotApproved is enforceImagePolicy's sentinel for a policy
// rejection, distinct from a genuine store error — callers use errors.Is
// to return 403 vs 500 accordingly.
var errImageNotApproved = errors.New("image not approved")

// enforceImagePolicy rejects images that don't match an approved_images
// pattern, when the policy is enabled. A no-op when the policy is
// disabled. Called from handleCreateContainer/handleRecreateContainer
// (containers.go) and handleCreateDeployment (deployments.go) before
// dispatching to an agent.
func enforceImagePolicy(ctx context.Context, st *store.Store, images []string, trustedBuildTags ...string) error {
	enabled, err := st.GetImagePolicyEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	for _, ref := range images {
		// Only tags planned from a linked repository in this rollout are
		// trusted. A matching name alone could point to a public image.
		if slices.Contains(trustedBuildTags, ref) {
			continue
		}
		approved, err := st.IsImageApproved(ctx, ref)
		if err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("%w: %s", errImageNotApproved, ref)
		}
	}
	return nil
}
