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
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

const enrollmentTokenTTL = 1 * time.Hour

// NewRouter builds the HTTP handler for the REST API.
//
// A permissive CORS policy is applied so the Flutter web build can call
// this API from its dev server origin during local development; this
// should be tightened before any non-local deployment.
func NewRouter(log *slog.Logger, st *store.Store, authMgr *auth.Manager) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", handleLogin(log, st, authMgr))

	mux.Handle("GET /api/servers", authMgr.RequireAuth(handleListServers(log, st)))
	mux.Handle("POST /api/servers/enroll-token", authMgr.RequireAuth(handleCreateEnrollmentToken(log, st)))

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

		token, err := authMgr.IssueToken(user.ID, user.Email)
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
