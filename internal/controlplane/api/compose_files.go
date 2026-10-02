package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/compose"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// composeFileResponse augments a stored compose file with the service
// names parsed out of its content, so the list screen can show "web, db,
// redis" without the client having to parse YAML itself.
type composeFileResponse struct {
	store.ComposeFile
	ServiceNames []string `json:"serviceNames"`
}

func toComposeFileResponse(f store.ComposeFile) composeFileResponse {
	return composeFileResponse{
		ComposeFile:  f,
		ServiceNames: compose.Parse(f.Content).ServiceNames,
	}
}

func handleListComposeFiles(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		files, err := st.ListComposeFiles(r.Context())
		if err != nil {
			log.Error("failed to list compose files", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		responses := make([]composeFileResponse, len(files))
		for i, f := range files {
			responses[i] = toComposeFileResponse(f)
		}
		writeJSON(w, http.StatusOK, responses)
	}
}

func handleGetComposeFile(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := st.GetComposeFile(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "compose file not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load compose file", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, toComposeFileResponse(*f))
	}
}

func handleCreateComposeFile(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		result := compose.Parse(req.Content)
		if !result.Valid {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": result.Errors})
			return
		}
		if compose.HasBuild(req.Content) {
			http.Error(w, "build: requires a Compose file imported from a Git repository", http.StatusBadRequest)
			return
		}

		var createdBy string
		if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
			createdBy = claims.UserID
		}

		id, err := st.CreateComposeFile(r.Context(), name, req.Content, createdBy)
		if errors.Is(err, store.ErrDuplicateComposeFileName) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			log.Error("failed to create compose file", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		f, err := st.GetComposeFile(r.Context(), id)
		if err != nil {
			log.Error("failed to reload created compose file", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		recordAudit(r, log, st, "compose_file.create", "compose_file", f.ID, "Compose file "+f.Name+" created", nil)
		writeJSON(w, http.StatusCreated, toComposeFileResponse(*f))
	}
}

func handleUpdateComposeFile(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		existing, err := st.GetComposeFile(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "compose file not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load compose file", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = existing.Name
		}
		content := req.Content
		if strings.TrimSpace(content) == "" {
			content = existing.Content
		}

		result := compose.Parse(content)
		if !result.Valid {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": result.Errors})
			return
		}
		if compose.HasBuild(content) {
			if existing.GitRepositoryID == nil {
				http.Error(w, "build: requires a Compose file linked to a Git repository", http.StatusBadRequest)
				return
			}
			if !actorFromRequest(r).IsAdmin {
				http.Error(w, "only admins can edit services with build:", http.StatusForbidden)
				return
			}
		}

		if err := st.UpdateComposeFile(r.Context(), id, name, content); err != nil {
			if errors.Is(err, store.ErrDuplicateComposeFileName) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			log.Error("failed to update compose file", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		f, err := st.GetComposeFile(r.Context(), id)
		if err != nil {
			log.Error("failed to reload updated compose file", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if f.Version != existing.Version || f.Name != existing.Name {
			recordAudit(r, log, st, "compose_file.update", "compose_file", f.ID, fmt.Sprintf("Compose file %s updated to version %d", f.Name, f.Version), nil)
		}
		writeJSON(w, http.StatusOK, toComposeFileResponse(*f))
	}
}

func handleDeleteComposeFile(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := st.DeleteComposeFile(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "compose file not found", http.StatusNotFound)
			return
		}
		var inUse *store.ComposeFileInUseError
		if errors.As(err, &inUse) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": inUse.Error()})
			return
		}
		if err != nil {
			log.Error("failed to delete compose file", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		recordAudit(r, log, st, "compose_file.delete", "compose_file", r.PathValue("id"), "Compose file deleted", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListComposeFileVersions lists a compose file's version history —
// "Maintain Compose file version history".
func handleListComposeFileVersions(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		if _, err := st.GetComposeFile(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "compose file not found", http.StatusNotFound)
				return
			}
			log.Error("failed to load compose file", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		versions, err := st.ListComposeFileVersions(r.Context(), id)
		if err != nil {
			log.Error("failed to list compose file versions", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, versions)
	}
}

// handleGetComposeFileVersion returns one past version's full content —
// backs both viewing a version and "Compare two versions" (the client
// fetches two and diffs them locally).
func handleGetComposeFileVersion(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version, err := st.GetComposeFileVersion(r.Context(), r.PathValue("id"), r.PathValue("versionId"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "version not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load compose file version", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, version)
	}
}

// handleRestoreComposeFileVersion makes a past version the current
// content again, going through the normal UpdateComposeFile path — which
// itself snapshots whatever was current before the restore, so restoring
// is itself undoable.
func handleRestoreComposeFileVersion(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		version, err := st.GetComposeFileVersion(r.Context(), id, r.PathValue("versionId"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "version not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load compose file version", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if compose.HasBuild(version.Content) {
			if !actorFromRequest(r).IsAdmin {
				http.Error(w, "only admins can restore services with build:", http.StatusForbidden)
				return
			}
			file, err := st.GetComposeFile(r.Context(), id)
			if err != nil {
				writeActionError(w, log, err)
				return
			}
			if file.GitRepositoryID == nil {
				http.Error(w, "build: requires a Compose file linked to a Git repository", http.StatusBadRequest)
				return
			}
		}

		if err := st.UpdateComposeFile(r.Context(), id, version.Name, version.Content); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "compose file not found", http.StatusNotFound)
				return
			}
			if errors.Is(err, store.ErrDuplicateComposeFileName) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			log.Error("failed to restore compose file version", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		f, err := st.GetComposeFile(r.Context(), id)
		if err != nil {
			log.Error("failed to reload restored compose file", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		recordAudit(r, log, st, "compose_file.restore_version", "compose_file", f.ID, fmt.Sprintf("Compose file %s restored to version %d (now version %d)", f.Name, version.VersionNumber, f.Version), nil)
		writeJSON(w, http.StatusOK, toComposeFileResponse(*f))
	}
}

// handleParseComposeYAML backs both the YAML editor's live validation and
// the "switch to visual mode" hydration step — a single parse serves both,
// since visual hydration is just validation plus the projected services.
func handleParseComposeYAML(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Content string `json:"content"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, compose.Parse(req.Content))
	}
}

// handleRenderComposeYAML backs the visual editor's "switch to YAML mode"
// step, turning the in-progress form state into Compose YAML text.
func handleRenderComposeYAML(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Services []compose.ServiceDraft `json:"services"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		content, err := compose.Render(req.Services)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"content": content})
	}
}
