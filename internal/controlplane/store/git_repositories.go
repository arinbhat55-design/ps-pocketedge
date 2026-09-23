package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicateGitRepositoryName is returned when another repository
// already has that name.
var ErrDuplicateGitRepositoryName = errors.New("a Git repository with this name already exists")

// GitRepository is a Git remote that Compose files can be imported from.
// Token and WebhookSecret are never serialized to API clients (json:"-");
// HasToken tells the UI whether credentials are configured.
type GitRepository struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Provider      string    `json:"provider"`
	URL           string    `json:"url"`
	Username      string    `json:"username,omitempty"`
	Token         string    `json:"-"`
	HasToken      bool      `json:"hasToken"`
	DefaultBranch string    `json:"defaultBranch"`
	WebhookSecret string    `json:"-"`
	CreatedBy     *string   `json:"createdBy,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

const gitRepoColumns = `id, name, provider, url, username, token, default_branch, webhook_secret, created_by, created_at, updated_at`

func scanGitRepo(row pgx.Row) (*GitRepository, error) {
	var g GitRepository
	if err := row.Scan(&g.ID, &g.Name, &g.Provider, &g.URL, &g.Username, &g.Token, &g.DefaultBranch, &g.WebhookSecret, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, err
	}
	g.HasToken = g.Token != ""
	return &g, nil
}

func (s *Store) ListGitRepositories(ctx context.Context) ([]GitRepository, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+gitRepoColumns+` FROM git_repositories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GitRepository{}
	for rows.Next() {
		g, err := scanGitRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// GetGitRepository returns ErrNotFound if id doesn't exist.
func (s *Store) GetGitRepository(ctx context.Context, id string) (*GitRepository, error) {
	g, err := scanGitRepo(s.pool.QueryRow(ctx, `SELECT `+gitRepoColumns+` FROM git_repositories WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

func (s *Store) CreateGitRepository(ctx context.Context, g GitRepository, createdBy string) (string, error) {
	var by any
	if createdBy != "" {
		by = createdBy
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO git_repositories (name, provider, url, username, token, default_branch, webhook_secret, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id
	`, g.Name, g.Provider, g.URL, g.Username, g.Token, g.DefaultBranch, g.WebhookSecret, by).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return "", ErrDuplicateGitRepositoryName
	}
	return id, err
}

// UpdateGitRepository saves every field of g except WebhookSecret; the
// token is only replaced when replaceToken is set (so the UI can edit a
// repository without re-entering its credentials).
func (s *Store) UpdateGitRepository(ctx context.Context, g GitRepository, replaceToken bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE git_repositories SET name = $2, provider = $3, url = $4, username = $5,
			token = CASE WHEN $6 THEN $7 ELSE token END, default_branch = $8, updated_at = now()
		WHERE id = $1
	`, g.ID, g.Name, g.Provider, g.URL, g.Username, replaceToken, g.Token, g.DefaultBranch)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDuplicateGitRepositoryName
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RotateGitWebhookSecret(ctx context.Context, id, secret string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE git_repositories SET webhook_secret = $2, updated_at = now() WHERE id = $1`, id, secret)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteGitRepository removes a repository; linked Compose files keep their
// content but lose the link (ON DELETE SET NULL).
func (s *Store) DeleteGitRepository(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM git_repositories WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
