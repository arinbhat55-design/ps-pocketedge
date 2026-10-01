package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Deployment mirrors a row in the deployments table: one instance of a
// compose definition applied to a server. Exactly one of StackID (the
// curated stacks catalog) or ComposeFileID (a user-authored compose_files
// row) is set — see the deployments_exactly_one_source check constraint.
type Deployment struct {
	ID                string            `json:"id"`
	StackID           *string           `json:"stackId,omitempty"`
	ComposeFileID     *string           `json:"composeFileId,omitempty"`
	ServerID          string            `json:"serverId"`
	Env               map[string]string `json:"env"`
	Phase             string            `json:"phase"`
	CreatedBy         *string           `json:"createdBy,omitempty"`
	DeployEnvironment *string           `json:"deployEnvironment,omitempty"`
	Tags              []string          `json:"tags,omitempty"`
	// ChangeRequest/Notes are free-text governance metadata — "Change
	// request reference" and "Deployment notes" — editable the same way
	// as DeployEnvironment/Tags via UpdateDeploymentMetadata.
	ChangeRequest string `json:"changeRequest,omitempty"`
	Notes         string `json:"notes,omitempty"`
	// RollbackPlan is free-text "how to back this out" governance
	// metadata; AutoRollback makes a failed post-deployment health
	// verification automatically redeploy the last healthy revision.
	RollbackPlan string `json:"rollbackPlan,omitempty"`
	AutoRollback bool   `json:"autoRollback"`
	// Scales holds per-service replica overrides; a service not listed
	// runs its Compose-declared count. UpdateStrategy is "recreate" or
	// "rolling" (see agent docker.Deploy).
	Scales         map[string]int `json:"scales"`
	UpdateStrategy string         `json:"updateStrategy"`
	// GitRef is the branch/tag a Git-linked deployment tracks (empty = the
	// Compose file's own ref); AutoDeploy lets a push webhook redeploy it.
	GitRef     string `json:"gitRef,omitempty"`
	AutoDeploy bool   `json:"autoDeploy"`
	// HealthStatus is the current revision's post-deployment health
	// verification result: unknown, verifying, healthy, or unhealthy.
	HealthStatus    string     `json:"healthStatus"`
	HealthMessage   string     `json:"healthMessage,omitempty"`
	HealthCheckedAt *time.Time `json:"healthCheckedAt,omitempty"`
	CurrentRevision int        `json:"currentRevision"`
	PromotedFrom    *string    `json:"promotedFrom,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// deploymentColumns is the SELECT list scanDeployment reads, in order.
const deploymentColumns = `d.id, d.stack_id, d.compose_file_id, d.server_id, d.env, d.phase, d.created_by, d.deploy_environment, d.tags,
	d.change_request, d.notes, d.rollback_plan, d.auto_rollback, d.scales, d.update_strategy, d.git_ref, d.auto_deploy,
	d.health_status, d.health_message, d.health_checked_at, d.current_revision, d.promoted_from, d.created_at, d.updated_at`

func scanDeployment(row pgx.Row, extra ...any) (*Deployment, error) {
	var d Deployment
	var env, scales []byte
	dest := []any{&d.ID, &d.StackID, &d.ComposeFileID, &d.ServerID, &env, &d.Phase, &d.CreatedBy, &d.DeployEnvironment, &d.Tags,
		&d.ChangeRequest, &d.Notes, &d.RollbackPlan, &d.AutoRollback, &scales, &d.UpdateStrategy, &d.GitRef, &d.AutoDeploy,
		&d.HealthStatus, &d.HealthMessage, &d.HealthCheckedAt, &d.CurrentRevision, &d.PromotedFrom, &d.CreatedAt, &d.UpdatedAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(env, &d.Env); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(scales, &d.Scales); err != nil {
		return nil, err
	}
	if d.Scales == nil {
		d.Scales = map[string]int{}
	}
	return &d, nil
}

// DeploymentEvent mirrors a row in the append-only deployment_events log.
// TriggeredBy/TriggeredByEmail are unset for agent-reported phase events
// (a deploy's own pull/create/running/failed progression), which have no
// human actor — only handler-initiated events (create, redeploy, rollback,
// stack actions) carry one. Part of "Complete audit trail".
type DeploymentEvent struct {
	ID               int64   `json:"id"`
	DeploymentID     string  `json:"deploymentId"`
	Phase            string  `json:"phase"`
	Message          string  `json:"message"`
	TriggeredBy      *string `json:"triggeredBy,omitempty"`
	TriggeredByEmail *string `json:"triggeredByEmail,omitempty"`
	// Service is set for a per-service progress event (one service's image
	// pulled, container started, ...) and empty for whole-stack events.
	Service   string    `json:"service,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// NewDeployment is everything InsertDeployment needs for a new row.
// Exactly one of StackID/ComposeFileID must be set. Phase defaults to
// "pending"; a deployment waiting for approval or a maintenance window is
// inserted as "awaiting_approval"/"scheduled" instead.
type NewDeployment struct {
	StackID        *string
	ComposeFileID  *string
	ServerID       string
	Env            map[string]string
	CreatedBy      string
	Environment    *string
	Tags           []string
	ChangeRequest  string
	Notes          string
	RollbackPlan   string
	AutoRollback   bool
	Scales         map[string]int
	UpdateStrategy string
	GitRef         string
	AutoDeploy     bool
	PromotedFrom   *string
	Phase          string
}

// InsertDeployment inserts a new deployment row and returns its ID.
func (s *Store) InsertDeployment(ctx context.Context, n NewDeployment) (string, error) {
	payload, err := json.Marshal(n.Env)
	if err != nil {
		return "", err
	}
	if n.Scales == nil {
		n.Scales = map[string]int{}
	}
	scales, err := json.Marshal(n.Scales)
	if err != nil {
		return "", err
	}
	if n.Tags == nil {
		n.Tags = []string{}
	}
	if n.Phase == "" {
		n.Phase = "pending"
	}
	if n.UpdateStrategy == "" {
		n.UpdateStrategy = "recreate"
	}
	var createdBy any
	if n.CreatedBy != "" {
		createdBy = n.CreatedBy
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO deployments (stack_id, compose_file_id, server_id, env, phase, created_by, deploy_environment, tags,
			change_request, notes, rollback_plan, auto_rollback, scales, update_strategy, git_ref, auto_deploy, promoted_from)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id
	`, n.StackID, n.ComposeFileID, n.ServerID, payload, n.Phase, createdBy, n.Environment, n.Tags,
		n.ChangeRequest, n.Notes, n.RollbackPlan, n.AutoRollback, scales, n.UpdateStrategy, n.GitRef, n.AutoDeploy, n.PromotedFrom).Scan(&id)
	return id, err
}

// GetDeployment looks up a single deployment by ID. Returns ErrNotFound if
// it doesn't exist.
func (s *Store) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	d, err := scanDeployment(s.pool.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM deployments d WHERE d.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// DeploymentSummary is one row of the deployment history list: the
// deployment plus the names needed to show it without further lookups.
type DeploymentSummary struct {
	Deployment
	ServerName       string     `json:"serverName"`
	SourceName       string     `json:"sourceName"`
	CreatedByEmail   *string    `json:"createdByEmail,omitempty"`
	PendingRequests  int        `json:"pendingRequests"`
	LatestGitCommit  string     `json:"latestGitCommit,omitempty"`
	LatestRevisionAt *time.Time `json:"latestRevisionAt,omitempty"`
}

// DeploymentFilter narrows ListDeployments. Every non-empty field is ANDed.
type DeploymentFilter struct {
	Environment    string
	ServerID       string
	ComposeFileID  string
	Phase          string
	Search         string
	IncludeRemoved bool
	Limit          int
}

// ListDeployments is "Deployment history": every deployment, newest first,
// with its server/source names, owner email, number of pending
// approval/scheduled requests, and latest revision's Git commit.
func (s *Store) ListDeployments(ctx context.Context, f DeploymentFilter) ([]DeploymentSummary, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+deploymentColumns+`,
			COALESCE(srv.hostname, srv.name, ''), COALESCE(st.name, cf.name, ''), u.email,
			(SELECT count(*) FROM deployment_requests r WHERE r.deployment_id = d.id AND r.status IN ('pending_approval', 'scheduled')),
			COALESCE(rev.git_commit, ''), rev.created_at
		FROM deployments d
		LEFT JOIN servers srv ON srv.id = d.server_id
		LEFT JOIN stacks st ON st.id = d.stack_id
		LEFT JOIN compose_files cf ON cf.id = d.compose_file_id
		LEFT JOIN users u ON u.id = d.created_by
		LEFT JOIN deployment_revisions rev ON rev.deployment_id = d.id AND rev.revision = d.current_revision
		WHERE ($1 = '' OR d.deploy_environment = $1)
		  AND ($2 = '' OR d.server_id::text = $2)
		  AND ($3 = '' OR d.compose_file_id::text = $3)
		  AND ($4 = '' OR d.phase = $4)
		  AND ($5 = '' OR COALESCE(st.name, cf.name, '') ILIKE '%' || $5 || '%' OR d.change_request ILIKE '%' || $5 || '%')
		  AND ($6 OR d.phase <> 'removed')
		ORDER BY d.created_at DESC
		LIMIT $7
	`, f.Environment, f.ServerID, f.ComposeFileID, f.Phase, f.Search, f.IncludeRemoved, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DeploymentSummary{}
	for rows.Next() {
		var sum DeploymentSummary
		d, err := scanDeployment(rows, &sum.ServerName, &sum.SourceName, &sum.CreatedByEmail, &sum.PendingRequests, &sum.LatestGitCommit, &sum.LatestRevisionAt)
		if err != nil {
			return nil, err
		}
		sum.Deployment = *d
		out = append(out, sum)
	}
	return out, rows.Err()
}

// ListAutoDeployDeployments returns the non-removed deployments of
// composeFileID that have AutoDeploy on — what a Git push webhook
// redeploys.
func (s *Store) ListAutoDeployDeployments(ctx context.Context, composeFileID string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deploymentColumns+` FROM deployments d
		WHERE d.compose_file_id = $1 AND d.auto_deploy AND d.phase NOT IN ('removed', 'rejected')`, composeFileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ResolveDeploymentSource looks up the name and Compose YAML a deployment
// was created from, regardless of whether it came from the stacks catalog
// or a user-authored compose file — the one place that distinction is
// resolved, so redeploy/restore/backup call sites don't need to branch on
// it themselves.
func (s *Store) ResolveDeploymentSource(ctx context.Context, d *Deployment) (name string, composeYAML string, err error) {
	if d.StackID != nil {
		stack, err := s.GetStack(ctx, *d.StackID)
		if err != nil {
			return "", "", err
		}
		return stack.Name, stack.ComposeYAML, nil
	}
	if d.ComposeFileID != nil {
		file, err := s.GetComposeFile(ctx, *d.ComposeFileID)
		if err != nil {
			return "", "", err
		}
		// A Compose file with build: sections can't be sent to an agent as
		// written; what's deployable is the current revision's content,
		// with those sections replaced by the images built for it.
		if d.CurrentRevision > 0 {
			var pinned string
			err := s.pool.QueryRow(ctx, `
				SELECT compose_content FROM deployment_revisions
				WHERE deployment_id = $1 AND revision = $2 AND source_content <> ''
			`, d.ID, d.CurrentRevision).Scan(&pinned)
			if err == nil {
				return file.Name, pinned, nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return "", "", err
			}
		}
		return file.Name, file.Content, nil
	}
	return "", "", errors.New("deployment has no source (neither stack_id nor compose_file_id is set)")
}

// UpdateDeploymentPhase sets a deployment's current phase.
func (s *Store) UpdateDeploymentPhase(ctx context.Context, id, phase string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE deployments SET phase = $2, updated_at = now() WHERE id = $1
	`, id, phase)
	return err
}

// DeploymentMetadata is the editable grouping/governance/rollout metadata
// of a deployment — owner is fixed at creation time (created_by), but the
// rest is expected to be edited as a deployment's purpose becomes clearer
// (e.g. "prod" once promoted, or a change-request ID attached after the
// fact).
type DeploymentMetadata struct {
	Environment    *string
	Tags           []string
	ChangeRequest  string
	Notes          string
	RollbackPlan   string
	AutoRollback   bool
	UpdateStrategy string
	AutoDeploy     bool
	GitRef         string
}

// UpdateDeploymentMetadata replaces a deployment's DeploymentMetadata.
func (s *Store) UpdateDeploymentMetadata(ctx context.Context, id string, m DeploymentMetadata) error {
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.UpdateStrategy == "" {
		m.UpdateStrategy = "recreate"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE deployments SET deploy_environment = $2, tags = $3, change_request = $4, notes = $5, rollback_plan = $6,
			auto_rollback = $7, update_strategy = $8, auto_deploy = $9, git_ref = $10, updated_at = now()
		WHERE id = $1
	`, id, m.Environment, m.Tags, m.ChangeRequest, m.Notes, m.RollbackPlan, m.AutoRollback, m.UpdateStrategy, m.AutoDeploy, m.GitRef)
	return err
}

// UpdateDeploymentScales replaces a deployment's per-service replica
// overrides.
func (s *Store) UpdateDeploymentScales(ctx context.Context, id string, scales map[string]int) error {
	payload, err := json.Marshal(scales)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE deployments SET scales = $2, updated_at = now() WHERE id = $1`, id, payload)
	return err
}

// UpdateDeploymentHealth records the deployment's post-deployment health
// verification state (the revision's own status is set separately, via
// UpdateRevisionStatus).
func (s *Store) UpdateDeploymentHealth(ctx context.Context, id, status, message string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE deployments SET health_status = $2, health_message = $3, health_checked_at = now(), updated_at = now() WHERE id = $1
	`, id, status, message)
	return err
}

// AddServiceDeploymentEvent is AddDeploymentEvent for one service's
// progress (see DeploymentEvent.Service). Agent-reported, so no actor.
func (s *Store) AddServiceDeploymentEvent(ctx context.Context, deploymentID, service, phase, message string) (DeploymentEvent, error) {
	var e DeploymentEvent
	err := s.pool.QueryRow(ctx, `
		INSERT INTO deployment_events (deployment_id, phase, message, service)
		VALUES ($1, $2, $3, $4)
		RETURNING id, deployment_id, phase, message, service, created_at
	`, deploymentID, phase, message, service).Scan(&e.ID, &e.DeploymentID, &e.Phase, &e.Message, &e.Service, &e.CreatedAt)
	return e, err
}

// AddDeploymentEvent appends a row to the deployment's status history and
// returns the created row (with its generated id/created_at) so callers
// can publish it to live-status subscribers without a second round-trip.
// triggeredBy is the acting user's ID, or "" for an agent-reported event
// with no human actor (see DeploymentEvent's doc comment).
func (s *Store) AddDeploymentEvent(ctx context.Context, deploymentID, phase, message, triggeredBy string) (DeploymentEvent, error) {
	var triggeredByArg any
	if triggeredBy != "" {
		triggeredByArg = triggeredBy
	}
	var e DeploymentEvent
	err := s.pool.QueryRow(ctx, `
		INSERT INTO deployment_events (deployment_id, phase, message, triggered_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, deployment_id, phase, message, triggered_by, created_at
	`, deploymentID, phase, message, triggeredByArg).Scan(&e.ID, &e.DeploymentID, &e.Phase, &e.Message, &e.TriggeredBy, &e.CreatedAt)
	return e, err
}

// ListDeploymentEvents returns a deployment's status history, oldest
// first, with the triggering user's email joined in where there is one.
func (s *Store) ListDeploymentEvents(ctx context.Context, deploymentID string) ([]DeploymentEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.deployment_id, e.phase, e.message, e.triggered_by, u.email, e.service, e.created_at
		FROM deployment_events e
		LEFT JOIN users u ON u.id = e.triggered_by
		WHERE e.deployment_id = $1 ORDER BY e.created_at
	`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []DeploymentEvent{}
	for rows.Next() {
		var e DeploymentEvent
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.Phase, &e.Message, &e.TriggeredBy, &e.TriggeredByEmail, &e.Service, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
