package api

import (
	"log/slog"
	"net/http"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

func handleListStacks(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stacks, err := st.ListStacks(r.Context())
		if err != nil {
			log.Error("failed to list stacks", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if claims, ok := auth.ClaimsFromContext(r.Context()); ok && claims.Role == "viewer" {
			for i := range stacks {
				stacks[i].ComposeYAML = ""
				stacks[i].DefaultEnv = nil
			}
		}
		writeJSON(w, http.StatusOK, stacks)
	}
}
