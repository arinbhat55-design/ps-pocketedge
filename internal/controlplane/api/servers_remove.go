package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// connectionChecker is the slice of *deploy.Dispatcher handleRemoveServer
// needs, so tests can fake connectivity.
type connectionChecker interface {
	IsConnected(serverID string) bool
}

var _ connectionChecker = (*deploy.Dispatcher)(nil)

// handleRemoveServer serves DELETE /api/servers/{id}: retires a server
// that's no longer reporting (see store.RetireServer). A server whose agent
// is still connected is refused with 409, so a live machine can't be
// dropped from the fleet by accident — stop its agent first.
func handleRemoveServer(log *slog.Logger, st *store.Store, conns connectionChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		server, err := st.GetServer(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "server not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load server", "server_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if conns.IsConnected(id) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "this server's agent is still connected; stop the agent before removing the server",
			})
			return
		}

		if err := st.RetireServer(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "server not found", http.StatusNotFound)
				return
			}
			log.Error("failed to remove server", "server_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		recordAudit(r, log, st, "server.remove", "server", id,
			"Removed server "+server.Name, map[string]string{"hostname": server.Hostname})
		log.Info("server removed", "server_id", id, "name", server.Name)
		w.WriteHeader(http.StatusNoContent)
	}
}
