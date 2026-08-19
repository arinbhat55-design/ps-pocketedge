// Package api implements the control plane's REST/JSON API consumed by the
// Flutter dashboard.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// NewRouter builds the HTTP handler for the REST API.
//
// M2 scope: read-only server listing, no auth yet (JWT auth for the admin
// UI lands in M3). A permissive CORS policy is applied so the Flutter web
// build can call this API from its dev server origin during local
// development; this should be tightened before any non-local deployment.
func NewRouter(log *slog.Logger, st *store.Store) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/servers", func(w http.ResponseWriter, r *http.Request) {
		servers, err := st.ListServers(r.Context())
		if err != nil {
			log.Error("failed to list servers", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, servers)
	})

	return withCORS(mux)
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
