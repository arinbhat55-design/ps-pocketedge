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
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}

// DeploymentEvent mirrors a row in the append-only deployment_events log.
type DeploymentEvent struct {
	ID           int64     `json:"id"`
	DeploymentID string    `json:"deploymentId"`
	Phase        string    `json:"phase"`
	Message      string    `json:"message"`
	CreatedAt    time.Time `json:"createdAt"`
}

// CreateDeployment inserts a new deployment row sourced from the stacks
// catalog, in the 'pending' phase. environment/tags are optional grouping
// metadata a caller can set at creation time; they can also be changed
// later via UpdateDeploymentMetadata.
func (s *Store) CreateDeployment(ctx context.Context, stackID, serverID string, env map[string]string, createdBy string, environment *string, tags []string) (string, error) {
	return s.createDeployment(ctx, &stackID, nil, serverID, env, createdBy, environment, tags)
}

// CreateComposeDeployment is CreateDeployment's counterpart for a
// user-authored compose_files row rather than the curated stacks catalog.
func (s *Store) CreateComposeDeployment(ctx context.Context, composeFileID, serverID string, env map[string]string, createdBy string, environment *string, tags []string) (string, error) {
	return s.createDeployment(ctx, nil, &composeFileID, serverID, env, createdBy, environment, tags)
}

func (s *Store) createDeployment(ctx context.Context, stackID, composeFileID *string, serverID string, env map[string]string, createdBy string, environment *string, tags []string) (string, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	if tags == nil {
		tags = []string{}
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO deployments (stack_id, compose_file_id, server_id, env, phase, created_by, deploy_environment, tags)
		VALUES ($1, $2, $3, $4, 'pending', $5, $6, $7)
		RETURNING id
	`, stackID, composeFileID, serverID, payload, createdBy, environment, tags).Scan(&id)
	return id, err
}

// GetDeployment looks up a single deployment by ID. Returns ErrNotFound if
// it doesn't exist.
func (s *Store) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	var d Deployment
	var env []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, stack_id, compose_file_id, server_id, env, phase, created_by, deploy_environment, tags, created_at, updated_at
		FROM deployments WHERE id = $1
	`, id).Scan(&d.ID, &d.StackID, &d.ComposeFileID, &d.ServerID, &env, &d.Phase, &d.CreatedBy, &d.DeployEnvironment, &d.Tags, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(env, &d.Env); err != nil {
		return nil, err
	}
	return &d, nil
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

// UpdateDeploymentMetadata sets a deployment's grouping metadata
// (environment/tags) after creation — owner is fixed at creation time
// (created_by), but environment/tags are expected to be edited as a
// deployment's purpose becomes clearer (e.g. "prod" once promoted).
func (s *Store) UpdateDeploymentMetadata(ctx context.Context, id string, environment *string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE deployments SET deploy_environment = $2, tags = $3, updated_at = now() WHERE id = $1
	`, id, environment, tags)
	return err
}

// AddDeploymentEvent appends a row to the deployment's status history and
// returns the created row (with its generated id/created_at) so callers
// can publish it to live-status subscribers without a second round-trip.
func (s *Store) AddDeploymentEvent(ctx context.Context, deploymentID, phase, message string) (DeploymentEvent, error) {
	var e DeploymentEvent
	err := s.pool.QueryRow(ctx, `
		INSERT INTO deployment_events (deployment_id, phase, message)
		VALUES ($1, $2, $3)
		RETURNING id, deployment_id, phase, message, created_at
	`, deploymentID, phase, message).Scan(&e.ID, &e.DeploymentID, &e.Phase, &e.Message, &e.CreatedAt)
	return e, err
}

// ListDeploymentEvents returns a deployment's status history, oldest first.
func (s *Store) ListDeploymentEvents(ctx context.Context, deploymentID string) ([]DeploymentEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, deployment_id, phase, message, created_at
		FROM deployment_events WHERE deployment_id = $1 ORDER BY created_at
	`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []DeploymentEvent{}
	for rows.Next() {
		var e DeploymentEvent
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.Phase, &e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
