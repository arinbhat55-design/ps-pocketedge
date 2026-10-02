package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// handleListSchedules lists schedules, optionally narrowed to one server
// and/or container.
func handleListSchedules(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		schedules, err := st.ListSchedules(r.Context(), store.ScheduleFilter{
			ServerID:    q.Get("serverId"),
			ContainerID: q.Get("containerId"),
		})
		if err != nil {
			log.Error("failed to list schedules", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, schedules)
	}
}

// handleCreateSchedule validates and creates a recurring (cron_expr) or
// one-time (runOnceAt) container start/stop schedule, computing its
// initial next_run_at.
func handleCreateSchedule(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		ServerID      string  `json:"serverId"`
		ContainerID   string  `json:"containerId"`
		ContainerName string  `json:"containerName"`
		Action        string  `json:"action"`
		ScheduleType  string  `json:"scheduleType"`
		CronExpr      *string `json:"cronExpr,omitempty"`
		RunOnceAt     *string `json:"runOnceAt,omitempty"` // RFC3339
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if req.ServerID == "" || req.ContainerID == "" || req.ContainerName == "" {
			http.Error(w, "serverId, containerId, and containerName are required", http.StatusBadRequest)
			return
		}
		if !isUUID(req.ServerID) {
			http.Error(w, "serverId "+req.ServerID+" is not a valid ID", http.StatusBadRequest)
			return
		}
		server, err := st.GetServer(r.Context(), req.ServerID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && server.Status == "removed") {
			http.Error(w, "server not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load server", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if req.Action != "start" && req.Action != "stop" {
			http.Error(w, `action must be "start" or "stop"`, http.StatusBadRequest)
			return
		}

		var (
			runOnceAt *time.Time
			nextRunAt time.Time
		)
		switch req.ScheduleType {
		case "recurring":
			if req.CronExpr == nil || *req.CronExpr == "" {
				http.Error(w, "cronExpr is required for a recurring schedule", http.StatusBadRequest)
				return
			}
			schedule, err := cron.ParseStandard(*req.CronExpr)
			if err != nil {
				http.Error(w, "invalid cronExpr: "+err.Error(), http.StatusBadRequest)
				return
			}
			nextRunAt = schedule.Next(time.Now())
		case "once":
			if req.RunOnceAt == nil || *req.RunOnceAt == "" {
				http.Error(w, "runOnceAt is required for a one-time schedule", http.StatusBadRequest)
				return
			}
			parsed, err := time.Parse(time.RFC3339, *req.RunOnceAt)
			if err != nil {
				http.Error(w, "invalid runOnceAt: "+err.Error(), http.StatusBadRequest)
				return
			}
			if !parsed.After(time.Now()) {
				http.Error(w, "runOnceAt must be in the future", http.StatusBadRequest)
				return
			}
			runOnceAt = &parsed
			nextRunAt = parsed
			req.CronExpr = nil
		default:
			http.Error(w, `scheduleType must be "recurring" or "once"`, http.StatusBadRequest)
			return
		}

		var createdBy string
		if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
			createdBy = claims.UserID
		}

		id, err := st.CreateSchedule(r.Context(), req.ServerID, req.ContainerID, req.ContainerName, req.Action, req.ScheduleType, req.CronExpr, runOnceAt, nextRunAt, createdBy)
		if err != nil {
			log.Error("failed to create schedule", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

// handleUpdateSchedule toggles a schedule's enabled state.
func handleUpdateSchedule(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Enabled bool `json:"enabled"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if err := st.UpdateScheduleEnabled(r.Context(), id, req.Enabled); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "schedule not found", http.StatusNotFound)
				return
			}
			log.Error("failed to update schedule", "schedule_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDeleteSchedule permanently removes a schedule.
func handleDeleteSchedule(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		if err := st.DeleteSchedule(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "schedule not found", http.StatusNotFound)
				return
			}
			log.Error("failed to delete schedule", "schedule_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
