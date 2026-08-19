package api

import (
	"log/slog"
	"net/http"

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
		writeJSON(w, http.StatusOK, stacks)
	}
}
