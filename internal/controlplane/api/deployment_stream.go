package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

var upgrader = websocket.Upgrader{
	// Matches the REST API's own permissive dev-mode CORS policy (see
	// withCORS) — the Flutter web build's dev-server origin varies, and
	// this should be tightened together with that before any non-local
	// deployment.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleDeploymentStream pushes a deployment's status history over a
// WebSocket, live: the full history on connect, then every new
// deployment_event as it's recorded, until the client disconnects. The
// stream deliberately doesn't end at a "terminal" phase: a rollout's
// per-service events, post-deployment health verification (which follows
// "running"), and later redeploys/rollbacks/scaling all keep appending to
// the same deployment's timeline.
//
// Browsers can't set custom headers on a WebSocket handshake, so unlike
// the rest of the API this endpoint takes the JWT as a `?token=` query
// parameter rather than an Authorization header — a deliberate, documented
// exception, not an oversight.
func handleDeploymentStream(log *slog.Logger, st *store.Store, authMgr *auth.Manager, events *deploy.EventBus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := authMgr.AuthenticateRequest(r, r.URL.Query().Get("token")); err != nil {
			auth.WriteAuthError(w, err)
			return
		}

		id := r.PathValue("id")
		if _, err := st.GetDeployment(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "deployment not found", http.StatusNotFound)
				return
			}
			log.Error("failed to load deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Subscribe before replaying history so no event published between
		// the history read and the subscribe call is missed.
		ch, unsubscribe := events.Subscribe(id)
		defer unsubscribe()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Warn("failed to upgrade deployment stream", "deployment_id", id, "error", err)
			return
		}
		defer conn.Close()

		history, err := st.ListDeploymentEvents(r.Context(), id)
		if err != nil {
			log.Error("failed to load deployment events", "deployment_id", id, "error", err)
			return
		}
		var lastSeenID int64
		for _, e := range history {
			if err := conn.WriteJSON(e); err != nil {
				return
			}
			lastSeenID = e.ID
		}

		// The client never sends anything meaningful on this connection,
		// but we still need to notice when it disconnects — WriteJSON
		// alone won't detect a closed read side promptly.
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
			case e := <-ch:
				if e.ID <= lastSeenID {
					// Already sent as part of the history replay above —
					// it landed in the DB before we read history but after
					// we subscribed.
					continue
				}
				if err := conn.WriteJSON(e); err != nil {
					return
				}
				lastSeenID = e.ID
			}
		}
	}
}
