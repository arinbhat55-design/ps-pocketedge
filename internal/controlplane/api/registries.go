package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/registryclient"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// handleListRegistries lists configured registries. store.Registry.Password
// is tagged json:"-" so credentials never round-trip back to the client.
func handleListRegistries(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		registries, err := st.ListRegistries(r.Context())
		if err != nil {
			log.Error("failed to list registries", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, registries)
	}
}

func handleCreateRegistry(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.URL == "" {
			http.Error(w, "name and url are required", http.StatusBadRequest)
			return
		}

		id, err := st.CreateRegistry(r.Context(), req.Name, req.URL, req.Username, req.Password)
		if err != nil {
			log.Error("failed to create registry", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

func handleDeleteRegistry(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := st.DeleteRegistry(r.Context(), id); err != nil {
			log.Error("failed to delete registry", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleSearchRegistries searches Docker Hub's public catalog (no
// ?registryId) or a configured private registry's catalog (?registryId=),
// per Registry Search's public-vs-private split.
func handleSearchRegistries(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		registryID := r.URL.Query().Get("registryId")

		if registryID == "" {
			results, err := registryclient.SearchDockerHub(r.Context(), query)
			if err != nil {
				log.Warn("docker hub search failed", "error", err)
				http.Error(w, "search failed: "+err.Error(), http.StatusBadGateway)
				return
			}
			writeJSON(w, http.StatusOK, results)
			return
		}

		reg, err := st.GetRegistry(r.Context(), registryID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "registry not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load registry", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		results, err := registryclient.SearchCatalog(r.Context(), reg.URL, query, registryclient.Credentials{Username: reg.Username, Password: reg.Password})
		if err != nil {
			log.Warn("private registry search failed", "registry_id", registryID, "error", err)
			http.Error(w, "search failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, results)
	}
}

// handleListImageTags lists the published tags for ?image=, for the
// "select image version or tag" picker.
func handleListImageTags(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		image := r.URL.Query().Get("image")
		if image == "" {
			http.Error(w, "image is required", http.StatusBadRequest)
			return
		}

		creds, err := resolveCreds(r, st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		tags, err := registryclient.ListTags(r.Context(), image, creds)
		if err != nil {
			log.Warn("failed to list tags", "image", image, "error", err)
			http.Error(w, "failed to list tags: "+err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, tags)
	}
}
