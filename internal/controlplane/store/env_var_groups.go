package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// validEnvironments are the profiles an env var group can be tagged with —
// "Development, test, staging, and production profiles".
var validEnvironments = map[string]bool{
	"development": true,
	"test":        true,
	"staging":     true,
	"production":  true,
}

// IsValidEnvironment reports whether env is one of the four supported
// deployment profiles.
func IsValidEnvironment(env string) bool {
	return validEnvironments[env]
}

// EnvVarGroup is one row in env_var_groups: a reusable, named set of
// environment variables tagged to a deployment profile. Variables reuses
// Stack's Parameter type — Default holds the actual value here (there's
// no separate "form default vs override" concept for a saved group), and
// Secret still marks a variable for obscured display, same as a catalog
// stack's parameters.
type EnvVarGroup struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Environment string      `json:"environment"`
	Variables   []Parameter `json:"variables"`
	CreatedBy   *string     `json:"createdBy,omitempty"`
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
}

// ListEnvVarGroups returns every group, optionally narrowed to one
// environment (empty string means all).
func (s *Store) ListEnvVarGroups(ctx context.Context, environment string) ([]EnvVarGroup, error) {
	query := `
		SELECT id, name, environment, variables, created_by, created_at, updated_at
		FROM env_var_groups
	`
	args := []any{}
	if environment != "" {
		query += ` WHERE environment = $1`
		args = append(args, environment)
	}
	query += ` ORDER BY environment, name`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := []EnvVarGroup{}
	for rows.Next() {
		g, err := scanEnvVarGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// GetEnvVarGroup looks up a single group by ID. Returns ErrNotFound if it
// doesn't exist.
func (s *Store) GetEnvVarGroup(ctx context.Context, id string) (*EnvVarGroup, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, name, environment, variables, created_by, created_at, updated_at
		FROM env_var_groups WHERE id = $1
	`, id)
	g, err := scanEnvVarGroup(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// CreateEnvVarGroup inserts a new group. createdBy may be empty.
func (s *Store) CreateEnvVarGroup(ctx context.Context, name, environment string, variables []Parameter, createdBy string) (string, error) {
	if variables == nil {
		variables = []Parameter{}
	}
	payload, err := json.Marshal(variables)
	if err != nil {
		return "", err
	}
	var createdByArg any
	if createdBy != "" {
		createdByArg = createdBy
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO env_var_groups (name, environment, variables, created_by)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, name, environment, payload, createdByArg).Scan(&id)
	return id, err
}

// UpdateEnvVarGroup overwrites an existing group's name, environment, and
// variables. Returns ErrNotFound if it doesn't exist.
func (s *Store) UpdateEnvVarGroup(ctx context.Context, id, name, environment string, variables []Parameter) error {
	if variables == nil {
		variables = []Parameter{}
	}
	payload, err := json.Marshal(variables)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE env_var_groups SET name = $2, environment = $3, variables = $4, updated_at = now()
		WHERE id = $1
	`, id, name, environment, payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteEnvVarGroup removes a group. Returns ErrNotFound if it doesn't
// exist.
func (s *Store) DeleteEnvVarGroup(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM env_var_groups WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanEnvVarGroup(row rowScanner) (EnvVarGroup, error) {
	var g EnvVarGroup
	var variables []byte
	err := row.Scan(&g.ID, &g.Name, &g.Environment, &variables, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return EnvVarGroup{}, err
	}
	if err := json.Unmarshal(variables, &g.Variables); err != nil {
		return EnvVarGroup{}, err
	}
	return g, nil
}
