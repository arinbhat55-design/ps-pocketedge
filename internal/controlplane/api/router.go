// Package api implements the control plane's REST/JSON API consumed by the
// Flutter dashboard.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/backup"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/livestate"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// defaultMetricsWindow is how far back GET .../metrics looks when the
// client doesn't pass a ?since= duration.
const defaultMetricsWindow = 1 * time.Hour

const enrollmentTokenTTL = 1 * time.Hour

// NewRouter builds the HTTP handler for the REST API.
//
// publicURL is how agents reach this control plane's HTTP API (used to
// build the upload/download URLs embedded in BackupCommand/RestoreCommand
// — the control plane can't assume its own bind address is what a remote
// agent, possibly behind a different network path or reverse proxy,
// should actually dial).
//
// A permissive CORS policy is applied so the Flutter web build can call
// this API from its dev server origin during local development; this
// should be tightened together with publicURL before any non-local
// deployment.
func NewRouter(log *slog.Logger, st *store.Store, authMgr *auth.Manager, dispatcher *deploy.Dispatcher, events *deploy.EventBus, serverEvents *livestate.EventBus, blobs *backup.BlobStore, publicURL string, inspectWaiter *deploy.InspectWaiter) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", handleLogin(log, st, authMgr))
	mux.Handle("GET /api/auth/me", authMgr.RequireAuth(handleGetMe(log, st)))
	mux.Handle("POST /api/auth/change-password", authMgr.RequireAuth(handleChangePassword(log, st)))

	mux.Handle("GET /api/users", authMgr.RequireAdmin(handleListUsers(log, st)))
	mux.Handle("POST /api/users", authMgr.RequireAdmin(handleCreateUser(log, st)))
	mux.Handle("PATCH /api/users/{id}/role", authMgr.RequireAdmin(handleUpdateUserRole(log, st)))
	mux.Handle("POST /api/users/{id}/reset-password", authMgr.RequireAdmin(handleResetUserPassword(log, st)))
	mux.Handle("DELETE /api/users/{id}", authMgr.RequireAdmin(handleDeleteUser(log, st)))

	mux.Handle("GET /api/servers", authMgr.RequireAuth(handleListServers(log, st)))
	mux.Handle("POST /api/servers/enroll-token", authMgr.RequireAuth(handleCreateEnrollmentToken(log, st)))
	mux.Handle("GET /api/servers/{id}", authMgr.RequireAuth(handleGetServer(log, st)))
	mux.Handle("GET /api/servers/{id}/metrics", authMgr.RequireAuth(handleGetServerMetrics(log, st)))
	// Auth via ?token= query param, not the Authorization header — see
	// handleDeploymentStream's doc comment for why.
	mux.HandleFunc("GET /api/servers/{id}/stream", handleServerStream(log, st, authMgr, serverEvents))

	mux.Handle("GET /api/stacks", authMgr.RequireAuth(handleListStacks(log, st)))

	mux.Handle("POST /api/deployments", authMgr.RequireAuth(handleCreateDeployment(log, st, dispatcher, events)))
	mux.Handle("GET /api/deployments/{id}", authMgr.RequireAuth(handleGetDeployment(log, st)))
	mux.Handle("PATCH /api/deployments/{id}/metadata", authMgr.RequireAuth(handleUpdateDeploymentMetadata(log, st)))
	mux.Handle("POST /api/deployments/{id}/redeploy", authMgr.RequireAuth(handleRedeployDeployment(log, st, dispatcher, events)))
	// Auth via ?token= query param, not the Authorization header — see
	// handleDeploymentStream's doc comment for why.
	mux.HandleFunc("GET /api/deployments/{id}/stream", handleDeploymentStream(log, st, authMgr, events))

	mux.Handle("GET /api/containers", authMgr.RequireAuth(handleListContainers(log, st)))
	mux.Handle("POST /api/servers/{id}/containers/{containerId}/inspect", authMgr.RequireAuth(handleInspectContainer(log, dispatcher, inspectWaiter)))

	mux.Handle("POST /api/deployments/{id}/backups", authMgr.RequireAuth(handleCreateBackup(log, st, dispatcher, publicURL)))
	mux.Handle("GET /api/deployments/{id}/backups", authMgr.RequireAuth(handleListBackups(log, st)))
	mux.Handle("GET /api/backups/{id}", authMgr.RequireAuth(handleGetBackup(log, st)))
	mux.Handle("POST /api/backups/{id}/restore", authMgr.RequireAuth(handleRestoreBackup(log, st, dispatcher, events, publicURL)))
	// Agent-credential auth (bearer token hashed against the owning
	// server's agent_token_hash), not admin JWT — these two are called by
	// the agent itself, not the Flutter app. See handleUploadBackupBlob's
	// doc comment.
	mux.HandleFunc("PUT /api/agent/backups/{id}/blob", handleUploadBackupBlob(log, st, blobs))
	mux.HandleFunc("GET /api/agent/backups/{id}/blob", handleDownloadBackupBlob(log, st, blobs))

	return withCORS(mux)
}

func handleLogin(log *slog.Logger, st *store.Store, authMgr *auth.Manager) http.HandlerFunc {
	type request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	type response struct {
		Token string `json:"token"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		user, err := st.GetUserByEmail(r.Context(), req.Email)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "invalid email or password", http.StatusUnauthorized)
			return
		}
		if err != nil {
			log.Error("login lookup failed", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if !auth.CheckPassword(user.PasswordHash, req.Password) {
			http.Error(w, "invalid email or password", http.StatusUnauthorized)
			return
		}

		token, err := authMgr.IssueToken(user.ID, user.Email, user.Role)
		if err != nil {
			log.Error("failed to issue token", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, response{Token: token})
	}
}

func handleListServers(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		servers, err := st.ListServers(r.Context())
		if err != nil {
			log.Error("failed to list servers", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, servers)
	}
}

func handleGetServer(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type response struct {
		store.Server
		Containers []store.ContainerState `json:"containers"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		server, err := st.GetServer(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "server not found", http.StatusNotFound)
				return
			}
			log.Error("failed to load server", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		containers, err := st.ListContainers(r.Context(), id)
		if err != nil {
			log.Error("failed to list containers", "server_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, response{Server: *server, Containers: containers})
	}
}

func handleGetServerMetrics(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		window := defaultMetricsWindow
		if raw := r.URL.Query().Get("since"); raw != "" {
			parsed, err := time.ParseDuration(raw)
			if err != nil {
				http.Error(w, "invalid since duration", http.StatusBadRequest)
				return
			}
			window = parsed
		}

		samples, err := st.ListMetricSamples(r.Context(), id, time.Now().Add(-window))
		if err != nil {
			log.Error("failed to list metric samples", "server_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, samples)
	}
}

func handleCreateEnrollmentToken(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type response struct {
		Token       string    `json:"token"`
		ExpiresAt   time.Time `json:"expiresAt"`
		InstallHint string    `json:"installHint"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		token, err := auth.RandomToken()
		if err != nil {
			log.Error("failed to generate enrollment token", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		expiresAt := time.Now().Add(enrollmentTokenTTL)
		if _, err := st.CreateEnrollmentToken(r.Context(), auth.HashToken(token), claims.UserID, expiresAt); err != nil {
			log.Error("failed to persist enrollment token", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, response{
			Token:       token,
			ExpiresAt:   expiresAt,
			InstallHint: "curl -sSL https://<control-plane>/install.sh | sh -s -- --token=" + token,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
