package store

import (
	"context"
	"encoding/json"
	"time"
)

// AuditEvent is one row of the audit trail: who did what to which entity.
// Unlike deployment_events (a deployment's own status timeline, including
// agent-reported phases), this covers every user-initiated change across
// Compose files, variable groups, deployments, approvals, environment
// policies, and Git repositories.
type AuditEvent struct {
	ID         int64   `json:"id"`
	ActorID    *string `json:"actorId,omitempty"`
	ActorEmail *string `json:"actorEmail,omitempty"`
	// LocalSession marks changes made through the no-login local session,
	// which borrows an administrator account.
	LocalSession bool            `json:"localSession"`
	Action       string          `json:"action"`
	EntityType   string          `json:"entityType"`
	EntityID     string          `json:"entityId"`
	Summary      string          `json:"summary"`
	Details      json.RawMessage `json:"details"`
	CreatedAt    time.Time       `json:"createdAt"`
}

// RecordAudit appends an audit event. actorID may be empty for
// system-initiated changes (a webhook, the scheduler). localSession marks a
// change made through the no-login local session. details may be nil.
func (s *Store) RecordAudit(ctx context.Context, actorID string, localSession bool, action, entityType, entityID, summary string, details any) error {
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var actor any
	if actorID != "" {
		actor = actorID
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO audit_events (actor_id, local_session, action, entity_type, entity_id, summary, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, actor, localSession, action, entityType, entityID, summary, payload)
	return err
}

// AuditFilter narrows ListAuditEvents; empty fields match everything.
type AuditFilter struct {
	EntityType string
	EntityID   string
	ActorID    string
	Search     string
	Before     int64
	Limit      int
}

// ListAuditEvents returns audit events newest first. Before (an event ID)
// pages backwards through older events.
func (s *Store) ListAuditEvents(ctx context.Context, f AuditFilter) ([]AuditEvent, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.actor_id, u.email, a.local_session, a.action, a.entity_type, a.entity_id, a.summary, a.details, a.created_at
		FROM audit_events a LEFT JOIN users u ON u.id = a.actor_id
		WHERE ($1 = '' OR a.entity_type = $1)
		  AND ($2 = '' OR a.entity_id = $2)
		  AND ($3 = '' OR a.actor_id::text = $3)
		  AND ($4 = '' OR a.summary ILIKE '%' || $4 || '%' OR a.action ILIKE '%' || $4 || '%')
		  AND ($5 = 0 OR a.id < $5)
		ORDER BY a.id DESC LIMIT $6
	`, f.EntityType, f.EntityID, f.ActorID, f.Search, f.Before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		var details []byte
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.LocalSession, &e.Action, &e.EntityType, &e.EntityID, &e.Summary, &details, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Details = details
		out = append(out, e)
	}
	return out, rows.Err()
}
