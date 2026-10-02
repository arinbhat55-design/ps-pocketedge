package api

import (
	"context"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

func handleReadiness(ping func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"service": "pspocketedge", "status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"service": "pspocketedge", "status": "ready", "version": version.Version})
	}
}
