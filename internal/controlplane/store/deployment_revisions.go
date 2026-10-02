package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// DeploymentRevision is one rollout actually dispatched to an agent — the
// exact Compose content, env, replica overrides, and Git commit it ran
// with. Revisions are numbered from 1 per deployment; the deployment's
// current_revision points at the latest one.
type DeploymentRevision struct {
	ID             string            `json:"id"`
	DeploymentID   string            `json:"deploymentId"`
	Revision       int               `json:"revision"`
	Action         string            `json:"action"`
	ComposeContent string            `json:"composeContent,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Scales         map[string]int    `json:"scales"`
	ComposeVersion *int              `json:"composeVersion,omitempty"`
	GitRef         string            `json:"gitRef,omitempty"`
	GitCommit      string            `json:"gitCommit,omitempty"`
	// SourceContent is the Compose content as written, with build:
	// sections, when ComposeContent had them replaced by the image tags
	// they were built as; empty when nothing was built.
	SourceContent string `json:"sourceContent,omitempty"`
	// ChangeSummary describes what this revision changed compared with
	// the one before it (code, configuration, images).
	ChangeSummary string `json:"changeSummary,omitempty"`
	Strategy      string `json:"strategy"`
	// Status: dispatched, running, failed, healthy, unhealthy, rolled_back,
	// or superseded (a newer rollout replaced it while its images were
	// still building, so it never reached the server).
	Status                string    `json:"status"`
	StatusMessage         string    `json:"statusMessage,omitempty"`
	AutoRollbackAttempted bool      `json:"autoRollbackAttempted"`
	CreatedBy             *string   `json:"createdBy,omitempty"`
	CreatedByEmail        *string   `json:"createdByEmail,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

// NewRevision is what RecordRevision stores for one dispatched rollout.
type NewRevision struct {
	DeploymentID   string
	Action         string
	ComposeContent string
	Env            map[string]string
	Scales         map[string]int
	ComposeVersion *int
	GitRef         string
	GitCommit      string
	SourceContent  string
	ChangeSummary  string
	Strategy       string
	CreatedBy      string
}

// RecordRevision appends the next revision for a deployment, points the
// deployment's current_revision at it, and resets the deployment's health
// to "unknown" (the new revision hasn't been verified yet) — all in one
// transaction so two concurrent rollouts can't claim the same number.
func (s *Store) RecordRevision(ctx context.Context, n NewRevision) (int, error) {
	env, err := json.Marshal(n.Env)
	if err != nil {
		return 0, err
	}
	if n.Scales == nil {
		n.Scales = map[string]int{}
	}
	scales, err := json.Marshal(n.Scales)
	if err != nil {
		return 0, err
	}
	var createdBy any
	if n.CreatedBy != "" {
		createdBy = n.CreatedBy
	}
	if n.Strategy == "" {
		n.Strategy = "recreate"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Lock the deployment row so concurrent rollouts serialize, then take
	// the next number after the highest revision ever recorded (not
	// current_revision, which moves back after a rolled-back rollout).
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id FROM deployments WHERE id = $1 FOR UPDATE`, n.DeploymentID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	var next int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision), 0) + 1 FROM deployment_revisions WHERE deployment_id = $1`, n.DeploymentID).Scan(&next); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO deployment_revisions (deployment_id, revision, action, compose_content, env, scales, compose_version, git_ref, git_commit, source_content, change_summary, strategy, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, n.DeploymentID, next, n.Action, n.ComposeContent, env, scales, n.ComposeVersion, n.GitRef, n.GitCommit, n.SourceContent, n.ChangeSummary, n.Strategy, createdBy); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE deployments SET current_revision = $2, health_status = 'unknown', health_message = '', health_checked_at = NULL, updated_at = now()
		WHERE id = $1
	`, n.DeploymentID, next); err != nil {
		return 0, err
	}
	return next, tx.Commit(ctx)
}

// SourceOf is the Compose content a revision was deployed from, as
// written: SourceContent when images were built for it, else
// ComposeContent itself.
func (r *DeploymentRevision) SourceOf() string {
	if r.SourceContent != "" {
		return r.SourceContent
	}
	return r.ComposeContent
}

// RevertToPreviousRevision points a deployment back at the most recent
// revision before its current one that was actually running (after a
// rolling update restored the previous containers), restoring that
// revision's health and setting the phase back to running. A no-op when
// there's no such revision.
func (s *Store) RevertToPreviousRevision(ctx context.Context, deploymentID string) error {
	_, err := s.pool.Exec(ctx, `
		WITH prev AS (
			SELECT r.revision, r.status, r.status_message FROM deployment_revisions r
			JOIN deployments d ON d.id = r.deployment_id
			WHERE r.deployment_id = $1 AND r.revision < d.current_revision AND r.status IN ('healthy', 'unhealthy', 'running')
			ORDER BY r.revision DESC LIMIT 1
		)
		UPDATE deployments SET current_revision = prev.revision, phase = 'running',
			health_status = CASE WHEN prev.status IN ('healthy', 'unhealthy') THEN prev.status ELSE 'unknown' END,
			health_message = prev.status_message, updated_at = now()
		FROM prev WHERE deployments.id = $1
	`, deploymentID)
	return err
}

// UpdateRevisionStatus sets one revision's status. revision 0 (a status
// from an agent that doesn't echo revisions) means the current revision.
func (s *Store) UpdateRevisionStatus(ctx context.Context, deploymentID string, revision int, status, message string) error {
	if revision == 0 {
		return s.UpdateCurrentRevisionStatus(ctx, deploymentID, status, message)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE deployment_revisions SET status = $3, status_message = $4, updated_at = now()
		WHERE deployment_id = $1 AND revision = $2
	`, deploymentID, revision, status, message)
	return err
}

// IsCurrentRevision reports whether revision is (still) the deployment's
// current one — a status for a superseded rollout must not overwrite the
// deployment's phase/health. revision 0 counts as current.
func (s *Store) IsCurrentRevision(ctx context.Context, deploymentID string, revision int) (bool, error) {
	if revision == 0 {
		return true, nil
	}
	var current int
	if err := s.pool.QueryRow(ctx, `SELECT current_revision FROM deployments WHERE id = $1`, deploymentID).Scan(&current); err != nil {
		return false, err
	}
	return current == revision, nil
}

// UpdateCurrentRevisionStatus sets the status of a deployment's current
// revision (running/failed as the agent reports them).
func (s *Store) UpdateCurrentRevisionStatus(ctx context.Context, deploymentID, status, message string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE deployment_revisions SET status = $2, status_message = $3, updated_at = now()
		WHERE deployment_id = $1 AND revision = (SELECT current_revision FROM deployments WHERE id = $1)
	`, deploymentID, status, message)
	return err
}

const revisionColumns = `r.id, r.deployment_id, r.revision, r.action, r.compose_content, r.env, r.scales, r.compose_version, r.git_ref, r.git_commit,
	r.source_content, r.change_summary, r.strategy, r.status, r.status_message, r.auto_rollback_attempted, r.created_by, u.email, r.created_at, r.updated_at`

func scanRevision(row pgx.Row) (*DeploymentRevision, error) {
	var r DeploymentRevision
	var env, scales []byte
	if err := row.Scan(&r.ID, &r.DeploymentID, &r.Revision, &r.Action, &r.ComposeContent, &env, &scales, &r.ComposeVersion, &r.GitRef, &r.GitCommit,
		&r.SourceContent, &r.ChangeSummary, &r.Strategy, &r.Status, &r.StatusMessage, &r.AutoRollbackAttempted, &r.CreatedBy, &r.CreatedByEmail, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(env, &r.Env); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(scales, &r.Scales); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListDeploymentRevisions returns a deployment's revisions, newest first,
// without their Compose content and env (see GetDeploymentRevision).
func (s *Store) ListDeploymentRevisions(ctx context.Context, deploymentID string) ([]DeploymentRevision, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+revisionColumns+` FROM deployment_revisions r
		LEFT JOIN users u ON u.id = r.created_by
		WHERE r.deployment_id = $1 ORDER BY r.revision DESC`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeploymentRevision{}
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		r.ComposeContent, r.SourceContent = "", ""
		r.Env = nil
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetDeploymentRevision looks up one revision by number, including its
// Compose content and env. Returns ErrNotFound if it doesn't exist.
func (s *Store) GetDeploymentRevision(ctx context.Context, deploymentID string, revision int) (*DeploymentRevision, error) {
	r, err := scanRevision(s.pool.QueryRow(ctx, `SELECT `+revisionColumns+` FROM deployment_revisions r
		LEFT JOIN users u ON u.id = r.created_by
		WHERE r.deployment_id = $1 AND r.revision = $2`, deploymentID, revision))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// LastGoodRevision returns the most recent revision before `before` that
// ended up healthy (or, for revisions verified before health checks
// existed, running) — the automatic rollback target and the default
// "rollback plan". Returns ErrNotFound if there's none.
func (s *Store) LastGoodRevision(ctx context.Context, deploymentID string, before int) (*DeploymentRevision, error) {
	r, err := scanRevision(s.pool.QueryRow(ctx, `SELECT `+revisionColumns+` FROM deployment_revisions r
		LEFT JOIN users u ON u.id = r.created_by
		WHERE r.deployment_id = $1 AND r.revision < $2 AND r.status IN ('healthy', 'running')
		ORDER BY (r.status = 'healthy') DESC, r.revision DESC LIMIT 1`, deploymentID, before))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// UnhealthyRevisionForAutoRollback is one deployment whose current revision
// failed health verification, has auto_rollback on, and hasn't had an
// automatic rollback attempted yet.
type UnhealthyRevisionForAutoRollback struct {
	DeploymentID string
	Revision     int
}

// ListPendingAutoRollbacks finds current revisions that are unhealthy with
// auto-rollback enabled and not yet attempted.
func (s *Store) ListPendingAutoRollbacks(ctx context.Context) ([]UnhealthyRevisionForAutoRollback, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, r.revision FROM deployments d
		JOIN deployment_revisions r ON r.deployment_id = d.id AND r.revision = d.current_revision
		WHERE d.auto_rollback AND d.health_status = 'unhealthy' AND NOT r.auto_rollback_attempted
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnhealthyRevisionForAutoRollback
	for rows.Next() {
		var u UnhealthyRevisionForAutoRollback
		if err := rows.Scan(&u.DeploymentID, &u.Revision); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MarkAutoRollbackAttempted flags a revision so the auto-rollback worker
// never acts on it twice. Returns false if it was already flagged (another
// worker got there first).
func (s *Store) MarkAutoRollbackAttempted(ctx context.Context, deploymentID string, revision int) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployment_revisions SET auto_rollback_attempted = true
		WHERE deployment_id = $1 AND revision = $2 AND NOT auto_rollback_attempted
	`, deploymentID, revision)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
