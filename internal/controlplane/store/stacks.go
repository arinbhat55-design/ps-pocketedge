package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Parameter is one user-fillable value a catalog stack exposes at deploy
// time (e.g. a database password) — rendered as a form field in the
// Flutter deploy flow, and merged into the deployment's env like any other
// override.
type Parameter struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Default string `json:"default"`
	// Secret marks a parameter for obscured input in the UI (e.g. a
	// password). It is not encryption — see the MVP plan's note that
	// deployments.env/stacks.default_env are plain JSONB at rest.
	Secret bool `json:"secret"`
}

// Stack mirrors a row in the stacks table: a deployable compose definition.
// Since M4 this doubled as the MVP's single hardcoded entry; this phase
// generalizes it into a small hand-curated catalog (not yet a full
// marketplace with versioning/search) spanning the product's three
// original categories — business apps, databases, and AI tooling.
type Stack struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	ComposeYAML string            `json:"composeYaml"`
	DefaultEnv  map[string]string `json:"defaultEnv"`
	Parameters  []Parameter       `json:"parameters"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// CreateStack inserts a new catalog entry.
func (s *Store) CreateStack(ctx context.Context, name, description, category, composeYAML string, defaultEnv map[string]string, parameters []Parameter) (string, error) {
	if defaultEnv == nil {
		defaultEnv = map[string]string{}
	}
	if parameters == nil {
		parameters = []Parameter{}
	}
	envPayload, err := json.Marshal(defaultEnv)
	if err != nil {
		return "", err
	}
	paramsPayload, err := json.Marshal(parameters)
	if err != nil {
		return "", err
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO stacks (name, description, category, compose_yaml, default_env, parameters)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, name, description, category, composeYAML, envPayload, paramsPayload).Scan(&id)
	return id, err
}

// ListStacks returns the full catalog.
func (s *Store) ListStacks(ctx context.Context) ([]Stack, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, category, compose_yaml, default_env, parameters, created_at
		FROM stacks ORDER BY category, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stacks := []Stack{}
	for rows.Next() {
		st, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		stacks = append(stacks, st)
	}
	return stacks, rows.Err()
}

// GetStack looks up a single catalog entry by ID. Returns ErrNotFound if
// it doesn't exist.
func (s *Store) GetStack(ctx context.Context, id string) (*Stack, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, name, description, category, compose_yaml, default_env, parameters, created_at
		FROM stacks WHERE id = $1
	`, id)
	st, err := scanStack(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// CountStacks returns how many catalog entries exist, used to decide
// whether to seed the starter catalog on first boot.
func (s *Store) CountStacks(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM stacks`).Scan(&count)
	return count, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanStack(row rowScanner) (Stack, error) {
	var st Stack
	var defaultEnv, parameters []byte
	err := row.Scan(&st.ID, &st.Name, &st.Description, &st.Category, &st.ComposeYAML, &defaultEnv, &parameters, &st.CreatedAt)
	if err != nil {
		return Stack{}, err
	}
	if err := json.Unmarshal(defaultEnv, &st.DefaultEnv); err != nil {
		return Stack{}, err
	}
	if err := json.Unmarshal(parameters, &st.Parameters); err != nil {
		return Stack{}, err
	}
	return st, nil
}
