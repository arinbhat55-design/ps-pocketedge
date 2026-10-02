package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Deployment request statuses.
const (
	RequestPendingApproval = "pending_approval"
	RequestScheduled       = "scheduled"
	RequestExecuted        = "executed"
	RequestRejected        = "rejected"
	RequestCancelled       = "cancelled"
	RequestFailed          = "failed"
)

// DeploymentRequest is a change to a deployment that couldn't run
// immediately — waiting for approval (the environment requires it), or
// approved but waiting for the environment's next maintenance window.
// Action is deploy, redeploy, rollback, or scale; Params carries what the
// action needs (e.g. {"revision": 3} for a rollback).
type DeploymentRequest struct {
	ID               string          `json:"id"`
	DeploymentID     string          `json:"deploymentId"`
	Action           string          `json:"action"`
	Params           json.RawMessage `json:"params"`
	Status           string          `json:"status"`
	Reason           string          `json:"reason,omitempty"`
	RequestedBy      *string         `json:"requestedBy,omitempty"`
	RequestedByEmail *string         `json:"requestedByEmail,omitempty"`
	RequestedAt      time.Time       `json:"requestedAt"`
	DecidedBy        *string         `json:"decidedBy,omitempty"`
	DecidedByEmail   *string         `json:"decidedByEmail,omitempty"`
	DecidedAt        *time.Time      `json:"decidedAt,omitempty"`
	DecisionComment  string          `json:"decisionComment,omitempty"`
	ScheduledFor     *time.Time      `json:"scheduledFor,omitempty"`
	ExecutedAt       *time.Time      `json:"executedAt,omitempty"`
	ResultMessage    string          `json:"resultMessage,omitempty"`
	// Joined in for list views.
	SourceName    string  `json:"sourceName,omitempty"`
	ServerName    string  `json:"serverName,omitempty"`
	Environment   *string `json:"environment,omitempty"`
	ChangeRequest string  `json:"changeRequest,omitempty"`
}

const requestColumns = `r.id, r.deployment_id, r.action, r.params, r.status, r.reason, r.requested_by, ru.email, r.requested_at,
	r.decided_by, du.email, r.decided_at, r.decision_comment, r.scheduled_for, r.executed_at, r.result_message,
	COALESCE(st.name, cf.name, ''), COALESCE(srv.hostname, srv.name, ''), d.deploy_environment, d.change_request`

const requestJoins = `FROM deployment_requests r
	JOIN deployments d ON d.id = r.deployment_id
	LEFT JOIN users ru ON ru.id = r.requested_by
	LEFT JOIN users du ON du.id = r.decided_by
	LEFT JOIN stacks st ON st.id = d.stack_id
	LEFT JOIN compose_files cf ON cf.id = d.compose_file_id
	LEFT JOIN servers srv ON srv.id = d.server_id`

func scanRequest(row pgx.Row) (*DeploymentRequest, error) {
	var r DeploymentRequest
	var params []byte
	if err := row.Scan(&r.ID, &r.DeploymentID, &r.Action, &params, &r.Status, &r.Reason, &r.RequestedBy, &r.RequestedByEmail, &r.RequestedAt,
		&r.DecidedBy, &r.DecidedByEmail, &r.DecidedAt, &r.DecisionComment, &r.ScheduledFor, &r.ExecutedAt, &r.ResultMessage,
		&r.SourceName, &r.ServerName, &r.Environment, &r.ChangeRequest); err != nil {
		return nil, err
	}
	r.Params = params
	return &r, nil
}

// CreateDeploymentRequest records a queued change and returns its ID.
func (s *Store) CreateDeploymentRequest(ctx context.Context, deploymentID, action string, params any, status, reason, requestedBy string, scheduledFor *time.Time) (string, error) {
	payload, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	var by any
	if requestedBy != "" {
		by = requestedBy
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO deployment_requests (deployment_id, action, params, status, reason, requested_by, scheduled_for)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id
	`, deploymentID, action, payload, status, reason, by, scheduledFor).Scan(&id)
	return id, err
}

// GetDeploymentRequest looks up one request. Returns ErrNotFound if it
// doesn't exist.
func (s *Store) GetDeploymentRequest(ctx context.Context, id string) (*DeploymentRequest, error) {
	r, err := scanRequest(s.pool.QueryRow(ctx, `SELECT `+requestColumns+` `+requestJoins+` WHERE r.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// ListDeploymentRequests returns requests, newest first, optionally
// narrowed to one status and/or one deployment.
func (s *Store) ListDeploymentRequests(ctx context.Context, status, deploymentID string) ([]DeploymentRequest, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+requestColumns+` `+requestJoins+`
		WHERE ($1 = '' OR r.status = $1) AND ($2 = '' OR r.deployment_id::text = $2)
		ORDER BY r.requested_at DESC LIMIT 500`, status, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeploymentRequest{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// DecideDeploymentRequest moves a pending_approval request to newStatus
// (scheduled, executed, or rejected), recording who decided and why.
// Returns false if the request wasn't pending_approval anymore (someone
// else decided first).
func (s *Store) DecideDeploymentRequest(ctx context.Context, id, newStatus, decidedBy, comment string, scheduledFor *time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployment_requests SET status = $2, decided_by = $3, decided_at = now(), decision_comment = $4, scheduled_for = $5
		WHERE id = $1 AND status = 'pending_approval'
	`, id, newStatus, decidedBy, comment, scheduledFor)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimDeploymentRequest atomically moves a request from one of `from` to
// "executed" (the caller is about to run it), so a request is never run
// twice by concurrent approvers/workers. Returns false if it wasn't in one
// of those statuses.
func (s *Store) ClaimDeploymentRequest(ctx context.Context, id string, from ...string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployment_requests SET status = 'executed', executed_at = now() WHERE id = $1 AND status = ANY($2)
	`, id, from)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// FinishDeploymentRequest records a claimed request's outcome; a failure
// flips its status from executed to failed.
func (s *Store) FinishDeploymentRequest(ctx context.Context, id string, failed bool, message string) error {
	status := RequestExecuted
	if failed {
		status = RequestFailed
	}
	_, err := s.pool.Exec(ctx, `UPDATE deployment_requests SET status = $2, result_message = $3 WHERE id = $1`, id, status, message)
	return err
}

// CancelDeploymentRequest cancels a still-waiting request. Returns false if
// it had already been decided or run.
func (s *Store) CancelDeploymentRequest(ctx context.Context, id, cancelledBy, comment string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployment_requests SET status = 'cancelled', decided_by = $2, decided_at = now(), decision_comment = $3
		WHERE id = $1 AND status IN ('pending_approval', 'scheduled')
	`, id, cancelledBy, comment)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ListDueScheduledRequests returns scheduled requests whose time has come.
func (s *Store) ListDueScheduledRequests(ctx context.Context, now time.Time) ([]DeploymentRequest, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+requestColumns+` `+requestJoins+`
		WHERE r.status = 'scheduled' AND r.scheduled_for <= $1 ORDER BY r.scheduled_for`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeploymentRequest{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// RescheduleDeploymentRequest moves a scheduled request to a later time
// (e.g. the window's policy changed and it's no longer in one).
func (s *Store) RescheduleDeploymentRequest(ctx context.Context, id string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE deployment_requests SET scheduled_for = $2 WHERE id = $1 AND status = 'scheduled'`, id, at)
	return err
}

// CountOpenRequests reports how many pending_approval/scheduled requests a
// deployment has.
func (s *Store) CountOpenRequests(ctx context.Context, deploymentID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM deployment_requests WHERE deployment_id = $1 AND status IN ('pending_approval', 'scheduled')`, deploymentID).Scan(&n)
	return n, err
}
