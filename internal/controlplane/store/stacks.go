package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Stack mirrors a row in the stacks table: a deployable compose definition.
// MVP: a small hand-seeded set, not a browsable marketplace catalog (that's
// a future phase — see the plan's "AI stack marketplace" section).
type Stack struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	ComposeYAML string            `json:"composeYaml"`
	DefaultEnv  map[string]string `json:"defaultEnv"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// CreateStack inserts a new stack definition.
func (s *Store) CreateStack(ctx context.Context, name, composeYAML string, defaultEnv map[string]string) (string, error) {
	payload, err := json.Marshal(defaultEnv)
	if err != nil {
		return "", err
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO stacks (name, compose_yaml, default_env)
		VALUES ($1, $2, $3)
		RETURNING id
	`, name, composeYAML, payload).Scan(&id)
	return id, err
}

// ListStacks returns all stack definitions.
func (s *Store) ListStacks(ctx context.Context) ([]Stack, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, compose_yaml, default_env, created_at FROM stacks ORDER BY created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stacks := []Stack{}
	for rows.Next() {
		var st Stack
		var defaultEnv []byte
		if err := rows.Scan(&st.ID, &st.Name, &st.ComposeYAML, &defaultEnv, &st.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(defaultEnv, &st.DefaultEnv); err != nil {
			return nil, err
		}
		stacks = append(stacks, st)
	}
	return stacks, rows.Err()
}

// GetStack looks up a single stack by ID. Returns ErrNotFound if it
// doesn't exist.
func (s *Store) GetStack(ctx context.Context, id string) (*Stack, error) {
	var st Stack
	var defaultEnv []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, compose_yaml, default_env, created_at FROM stacks WHERE id = $1
	`, id).Scan(&st.ID, &st.Name, &st.ComposeYAML, &defaultEnv, &st.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(defaultEnv, &st.DefaultEnv); err != nil {
		return nil, err
	}
	return &st, nil
}

// CountStacks returns how many stacks exist, used to decide whether to
// seed a starter stack on first boot.
func (s *Store) CountStacks(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stacks`).Scan(&count)
	return count, err
}
