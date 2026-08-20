package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/livestate"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// handleServerStream pushes a server's live resource/container state over a
// WebSocket: the current snapshot on connect, then every subsequent
// heartbeat's update, until the client disconnects. Unlike
// handleDeploymentStream, there's no history replay or terminal phase —
// this is a live gauge, not an event log (GET .../metrics is the history
// endpoint for that).
//
// Browsers can't set custom headers on a WebSocket handshake, so this
// endpoint takes the JWT as a `?token=` query parameter rather than an
// Authorization header, same documented exception as handleDeploymentStream.
func handleServerStream(log *slog.Logger, st *store.Store, authMgr *auth.Manager, serverEvents *livestate.EventBus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := authMgr.ParseToken(r.URL.Query().Get("token")); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

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

		// Subscribe before fetching the current state so no update
		// published between the fetch and the subscribe call is missed.
		ch, unsubscribe := serverEvents.Subscribe(id)
		defer unsubscribe()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Warn("failed to upgrade server stream", "server_id", id, "error", err)
			return
		}
		defer conn.Close()

		containers, err := st.ListContainers(r.Context(), id)
		if err != nil {
			log.Error("failed to list containers", "server_id", id, "error", err)
			return
		}
		// LastResources is nil until the server's first heartbeat, in which
		// case resources stays its zero value.
		var resources store.ResourceSnapshot
		_ = json.Unmarshal(server.LastResources, &resources)
		if err := conn.WriteJSON(livestate.ServerUpdate{
			Resources:  resources,
			Containers: containers,
			UpdatedAt:  time.Now(),
		}); err != nil {
			return
		}

		closed := make(chan struct{})
		go func() {
			for {
				if _, _, err := conn.NextReader(); err != nil {
					close(closed)
					return
				}
			}
		}()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-closed:
				return
			case update := <-ch:
				if err := conn.WriteJSON(update); err != nil {
					return
				}
			}
		}
	}
}
