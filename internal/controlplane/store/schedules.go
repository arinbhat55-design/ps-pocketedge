package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Schedule mirrors a row in the container_schedules table: a recurring
// (cron) or one-time start/stop action for a single container. Exactly one
// of CronExpr/RunOnceAt is set, matching ScheduleType. Everything is UTC.
type Schedule struct {
	ID            string     `json:"id"`
	ServerID      string     `json:"serverId"`
	ContainerID   string     `json:"containerId"`
	ContainerName string     `json:"containerName"`
	Action        string     `json:"action"`       // "start" or "stop"
	ScheduleType  string     `json:"scheduleType"` // "recurring" or "once"
	CronExpr      *string    `json:"cronExpr,omitempty"`
	RunOnceAt     *time.Time `json:"runOnceAt,omitempty"`
	NextRunAt     time.Time  `json:"nextRunAt"`
	LastRunAt     *time.Time `json:"lastRunAt,omitempty"`
	LastRunStatus *string    `json:"lastRunStatus,omitempty"`
	Enabled       bool       `json:"enabled"`
	CreatedBy     *string    `json:"createdBy,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

const scheduleColumns = `id, server_id, container_id, container_name, action, schedule_type,
	cron_expr, run_once_at, next_run_at, last_run_at, last_run_status, enabled, created_by, created_at`

func scanSchedule(row pgx.Row) (*Schedule, error) {
	var s Schedule
	if err := row.Scan(
		&s.ID, &s.ServerID, &s.ContainerID, &s.ContainerName, &s.Action, &s.ScheduleType,
		&s.CronExpr, &s.RunOnceAt, &s.NextRunAt, &s.LastRunAt, &s.LastRunStatus, &s.Enabled, &s.CreatedBy, &s.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateSchedule inserts a new schedule. Callers (the REST handler) are
// responsible for having already validated cronExpr/runOnceAt against
// scheduleType and computed the initial nextRunAt.
func (s *Store) CreateSchedule(ctx context.Context, serverID, containerID, containerName, action, scheduleType string, cronExpr *string, runOnceAt *time.Time, nextRunAt time.Time, createdBy string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO container_schedules
			(server_id, container_id, container_name, action, schedule_type, cron_expr, run_once_at, next_run_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, serverID, containerID, containerName, action, scheduleType, cronExpr, runOnceAt, nextRunAt, createdBy).Scan(&id)
	return id, err
}

// GetSchedule looks up a single schedule by ID. Returns ErrNotFound if it
// doesn't exist.
func (s *Store) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	sched, err := scanSchedule(s.pool.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM container_schedules WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sched, err
}

// ScheduleFilter narrows ListSchedules; zero fields are ignored.
type ScheduleFilter struct {
	ServerID    string
	ContainerID string
}

// ListSchedules returns schedules matching filter, most recently created
// first.
func (s *Store) ListSchedules(ctx context.Context, filter ScheduleFilter) ([]Schedule, error) {
	query := `SELECT ` + scheduleColumns + ` FROM container_schedules`
	var conditions []string
	var args []any
	add := func(cond string, arg any) {
		args = append(args, arg)
		conditions = append(conditions, fmt.Sprintf(cond, len(args)))
	}
	if filter.ServerID != "" {
		add("server_id = $%d", filter.ServerID)
	}
	if filter.ContainerID != "" {
		add("container_id = $%d", filter.ContainerID)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schedules := []Schedule{}
	for rows.Next() {
		sched, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, *sched)
	}
	return schedules, rows.Err()
}

// DueSchedules returns every enabled schedule whose next_run_at has
// arrived, for the scheduler's ticker to fire.
func (s *Store) DueSchedules(ctx context.Context, now time.Time) ([]Schedule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+scheduleColumns+`
		FROM container_schedules
		WHERE enabled AND next_run_at <= $1
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schedules := []Schedule{}
	for rows.Next() {
		sched, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, *sched)
	}
	return schedules, rows.Err()
}

// UpdateScheduleEnabled toggles a schedule on/off without touching its
// timing.
func (s *Store) UpdateScheduleEnabled(ctx context.Context, id string, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE container_schedules SET enabled = $2 WHERE id = $1`, id, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordRun records the outcome of firing a schedule. nextRunAt is the
// recurring schedule's next fire time; nil means "don't reschedule" (a
// one-time schedule that just fired), which also disables the schedule so
// it doesn't get picked up as due again.
func (s *Store) RecordRun(ctx context.Context, id, status string, nextRunAt *time.Time) error {
	if nextRunAt != nil {
		_, err := s.pool.Exec(ctx, `
			UPDATE container_schedules
			SET last_run_at = now(), last_run_status = $2, next_run_at = $3
			WHERE id = $1
		`, id, status, *nextRunAt)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE container_schedules
		SET last_run_at = now(), last_run_status = $2, enabled = false
		WHERE id = $1
	`, id, status)
	return err
}

// DeleteSchedule removes a schedule. Returns ErrNotFound if it doesn't
// exist.
func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM container_schedules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
