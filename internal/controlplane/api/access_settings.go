package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

type accessSettingsResponse struct {
	RequireLocalLogin bool `json:"requireLocalLogin"`
	LocalListener     bool `json:"localListener"`
}

func handleGetAccessSettings(log *slog.Logger, st *store.Store, localListener bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		required, err := st.RequireLocalLogin(r.Context())
		if err != nil {
			log.Error("failed to load access settings", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, accessSettingsResponse{required, localListener})
	}
}

func handleUpdateAccessSettings(log *slog.Logger, st *store.Store, authMgr *auth.Manager, localListener bool) http.HandlerFunc {
	type request struct {
		RequireLocalLogin *bool  `json:"requireLocalLogin"`
		Password          string `json:"password"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RequireLocalLogin == nil {
			http.Error(w, "requireLocalLogin is required", http.StatusBadRequest)
			return
		}
		if *req.RequireLocalLogin {
			claims, _ := auth.ClaimsFromContext(r.Context())
			user, err := st.GetUserByID(r.Context(), claims.UserID)
			if err != nil {
				log.Error("failed to load admin for access settings", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			if !auth.CheckPassword(user.PasswordHash, req.Password) {
				http.Error(w, "admin password is incorrect", http.StatusUnauthorized)
				return
			}
		}
		if err := st.SetRequireLocalLogin(r.Context(), *req.RequireLocalLogin); err != nil {
			log.Error("failed to update access settings", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		authMgr.SetLocalSessionsAllowed(localListener && !*req.RequireLocalLogin)
		writeJSON(w, http.StatusOK, accessSettingsResponse{*req.RequireLocalLogin, localListener})
	}
}
