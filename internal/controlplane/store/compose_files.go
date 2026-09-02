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
}

func (s *Store) ListComposeFiles(ctx context.Context) ([]ComposeFile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, content, version, created_by, created_at, updated_at
		FROM compose_files ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := []ComposeFile{}
	for rows.Next() {
		var f ComposeFile
		if err := rows.Scan(&f.ID, &f.Name, &f.Content, &f.Version, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// GetComposeFile looks up a single compose file by ID. Returns ErrNotFound
// if it doesn't exist.
func (s *Store) GetComposeFile(ctx context.Context, id string) (*ComposeFile, error) {
	var f ComposeFile
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, content, version, created_by, created_at, updated_at
		FROM compose_files WHERE id = $1
	`, id).Scan(&f.ID, &f.Name, &f.Content, &f.Version, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var currentName, currentContent string
	var currentVersion int
	var createdBy *string
	err = tx.QueryRow(ctx, `
		SELECT name, content, version, created_by FROM compose_files WHERE id = $1 FOR UPDATE
	`, id).Scan(&currentName, &currentContent, &currentVersion, &createdBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if currentName == name && currentContent == content {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO compose_file_versions (compose_file_id, version_number, name, content, created_by)
		VALUES ($1, $2, $3, $4, $5)
	`, id, currentVersion, currentName, currentContent, createdBy); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE compose_files SET name = $2, content = $3, version = version + 1, updated_at = now() WHERE id = $1
	`, id, name, content)
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
	CreatedBy     *string   `json:"createdBy,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ListComposeFileVersions returns composeFileID's version history, most
// recent first.
func (s *Store) ListComposeFileVersions(ctx context.Context, composeFileID string) ([]ComposeFileVersionSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, version_number, name, created_by, created_at
		FROM compose_file_versions WHERE compose_file_id = $1 ORDER BY version_number DESC
	`, composeFileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := []ComposeFileVersionSummary{}
	for rows.Next() {
		var v ComposeFileVersionSummary
		if err := rows.Scan(&v.ID, &v.VersionNumber, &v.Name, &v.CreatedBy, &v.CreatedAt); err != nil {
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
		SELECT id, compose_file_id, version_number, name, content, created_by, created_at
		FROM compose_file_versions WHERE id = $1 AND compose_file_id = $2
	`, versionID, composeFileID).Scan(&v.ID, &v.ComposeFileID, &v.VersionNumber, &v.Name, &v.Content, &v.CreatedBy, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
