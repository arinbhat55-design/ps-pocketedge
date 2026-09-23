package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicateComposeFileName is returned by CreateComposeFile and
// UpdateComposeFile when another compose file already has that name
// (unique_violation on compose_files.name).
var ErrDuplicateComposeFileName = errors.New("a compose file with this name already exists")

// ComposeFile is one row in compose_files: a user-authored Compose YAML
// document managed under Deployment Management > Docker Compose. Version
// starts at 1 and increments on every UpdateComposeFile call that actually
// changes name or content, each bump snapshotting the pre-update row into
// compose_file_versions.
type ComposeFile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	Version   int       `json:"version"`
	CreatedBy *string   `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Git link: set when the file was imported from (and is kept in sync
	// with) GitPath at GitRef in a Git repository. GitCommit is the commit
	// the current content came from.
	GitRepositoryID *string    `json:"gitRepositoryId,omitempty"`
	GitRef          string     `json:"gitRef,omitempty"`
	GitPath         string     `json:"gitPath,omitempty"`
	GitCommit       string     `json:"gitCommit,omitempty"`
	GitSyncedAt     *time.Time `json:"gitSyncedAt,omitempty"`
}

const composeFileColumns = `id, name, content, version, created_by, created_at, updated_at,
	git_repository_id, git_ref, git_path, git_commit, git_synced_at`

func scanComposeFile(row pgx.Row) (*ComposeFile, error) {
	var f ComposeFile
	err := row.Scan(&f.ID, &f.Name, &f.Content, &f.Version, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt,
		&f.GitRepositoryID, &f.GitRef, &f.GitPath, &f.GitCommit, &f.GitSyncedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) ListComposeFiles(ctx context.Context) ([]ComposeFile, error) {
	return s.queryComposeFiles(ctx, `SELECT `+composeFileColumns+` FROM compose_files ORDER BY name ASC`)
}

// ListComposeFilesByGitRepository returns the files linked to repoID —
// what a push webhook for that repository re-syncs.
func (s *Store) ListComposeFilesByGitRepository(ctx context.Context, repoID string) ([]ComposeFile, error) {
	return s.queryComposeFiles(ctx, `SELECT `+composeFileColumns+` FROM compose_files WHERE git_repository_id = $1 ORDER BY name`, repoID)
}

func (s *Store) queryComposeFiles(ctx context.Context, sql string, args ...any) ([]ComposeFile, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := []ComposeFile{}
	for rows.Next() {
		f, err := scanComposeFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, *f)
	}
	return files, rows.Err()
}

// GetComposeFile looks up a single compose file by ID. Returns ErrNotFound
// if it doesn't exist.
func (s *Store) GetComposeFile(ctx context.Context, id string) (*ComposeFile, error) {
	f, err := scanComposeFile(s.pool.QueryRow(ctx, `SELECT `+composeFileColumns+` FROM compose_files WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}

// CreateComposeFile inserts a new compose file. createdBy may be empty (no
// current user, e.g. during tests). Returns ErrDuplicateComposeFileName if
// the name is already taken.
func (s *Store) CreateComposeFile(ctx context.Context, name, content, createdBy string) (string, error) {
	var id string
	var createdByArg any
	if createdBy != "" {
		createdByArg = createdBy
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO compose_files (name, content, created_by) VALUES ($1, $2, $3) RETURNING id
	`, name, content, createdByArg).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return "", ErrDuplicateComposeFileName
	}
	return id, err
}

// UpdateComposeFile overwrites name and content for an existing compose
// file, first snapshotting its current name/content/version into
// compose_file_versions — unless name and content are both unchanged, in
// which case it's a no-op (no pointless version bump for e.g. re-saving
// the visual editor without edits). Returns ErrNotFound if the file
// doesn't exist, or ErrDuplicateComposeFileName if name collides with
// another file.
func (s *Store) UpdateComposeFile(ctx context.Context, id, name, content string) error {
	return s.updateComposeFile(ctx, id, name, content, nil)
}

// UpdateComposeFileFromGit is UpdateComposeFile for a Git sync: content
// comes from commit, which is recorded as the file's git_commit (and
// git_synced_at is bumped) even when the content itself didn't change.
func (s *Store) UpdateComposeFileFromGit(ctx context.Context, id, content, commit string) error {
	f, err := s.GetComposeFile(ctx, id)
	if err != nil {
		return err
	}
	return s.updateComposeFile(ctx, id, f.Name, content, &commit)
}

// SetComposeFileGitLink links (or, with repoID nil, unlinks) a Compose file
// to a path at a ref in a Git repository.
func (s *Store) SetComposeFileGitLink(ctx context.Context, id string, repoID *string, ref, path string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE compose_files SET git_repository_id = $2, git_ref = $3, git_path = $4,
			git_commit = CASE WHEN $2::uuid IS NULL THEN '' ELSE git_commit END, updated_at = now()
		WHERE id = $1
	`, id, repoID, ref, path)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) updateComposeFile(ctx context.Context, id, name, content string, gitCommit *string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var currentName, currentContent, currentCommit string
	var currentVersion int
	var createdBy *string
	err = tx.QueryRow(ctx, `
		SELECT name, content, version, created_by, git_commit FROM compose_files WHERE id = $1 FOR UPDATE
	`, id).Scan(&currentName, &currentContent, &currentVersion, &createdBy, &currentCommit)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if currentName == name && currentContent == content {
		if gitCommit != nil {
			if _, err := tx.Exec(ctx, `UPDATE compose_files SET git_commit = $2, git_synced_at = now() WHERE id = $1`, id, *gitCommit); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO compose_file_versions (compose_file_id, version_number, name, content, created_by, git_commit)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, currentVersion, currentName, currentContent, createdBy, currentCommit); err != nil {
		return err
	}

	if gitCommit != nil {
		_, err = tx.Exec(ctx, `
			UPDATE compose_files SET name = $2, content = $3, version = version + 1, git_commit = $4, git_synced_at = now(), updated_at = now() WHERE id = $1
		`, id, name, content, *gitCommit)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE compose_files SET name = $2, content = $3, version = version + 1, updated_at = now() WHERE id = $1
		`, id, name, content)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDuplicateComposeFileName
	}
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// DeleteComposeFile removes a compose file, cascading to its version
// history. Returns ErrNotFound if it doesn't exist.
func (s *Store) DeleteComposeFile(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM compose_files WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ComposeFileVersionSummary is one entry in a compose file's version
// history, without its content — kept light for listing (see
// GetComposeFileVersion for the full snapshot).
type ComposeFileVersionSummary struct {
	ID            string    `json:"id"`
	VersionNumber int       `json:"versionNumber"`
	Name          string    `json:"name"`
	GitCommit     string    `json:"gitCommit,omitempty"`
	CreatedBy     *string   `json:"createdBy,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ComposeFileVersion is one full snapshot from compose_file_versions,
// including its content — what GetComposeFileVersion returns for viewing,
// comparing, or restoring a past version.
type ComposeFileVersion struct {
	ID            string    `json:"id"`
	ComposeFileID string    `json:"composeFileId"`
	VersionNumber int       `json:"versionNumber"`
	Name          string    `json:"name"`
	Content       string    `json:"content"`
	GitCommit     string    `json:"gitCommit,omitempty"`
	CreatedBy     *string   `json:"createdBy,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ListComposeFileVersions returns composeFileID's version history, most
// recent first.
func (s *Store) ListComposeFileVersions(ctx context.Context, composeFileID string) ([]ComposeFileVersionSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, version_number, name, git_commit, created_by, created_at
		FROM compose_file_versions WHERE compose_file_id = $1 ORDER BY version_number DESC
	`, composeFileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := []ComposeFileVersionSummary{}
	for rows.Next() {
		var v ComposeFileVersionSummary
		if err := rows.Scan(&v.ID, &v.VersionNumber, &v.Name, &v.GitCommit, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// GetComposeFileVersion looks up one past version's full content. Returns
// ErrNotFound if no such version exists for composeFileID.
func (s *Store) GetComposeFileVersion(ctx context.Context, composeFileID, versionID string) (*ComposeFileVersion, error) {
	var v ComposeFileVersion
	err := s.pool.QueryRow(ctx, `
		SELECT id, compose_file_id, version_number, name, content, git_commit, created_by, created_at
		FROM compose_file_versions WHERE id = $1 AND compose_file_id = $2
	`, versionID, composeFileID).Scan(&v.ID, &v.ComposeFileID, &v.VersionNumber, &v.Name, &v.Content, &v.GitCommit, &v.CreatedBy, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
