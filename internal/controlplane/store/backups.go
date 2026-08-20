package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Backup mirrors a row in the backups table: a full volume-level snapshot
// of a deployment, taken on demand (no scheduling yet — see the plan's
// "Future Phases" note on this being a later addition).
type Backup struct {
	ID           string     `json:"id"`
	DeploymentID string     `json:"deploymentId"`
	ServerID     string     `json:"serverId"`
	Status       string     `json:"status"`
	Message      string     `json:"message"`
	SizeBytes    *int64     `json:"sizeBytes,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
}

// CreateBackup inserts a new backup row in the 'pending' phase.
func (s *Store) CreateBackup(ctx context.Context, deploymentID, serverID, createdBy string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO backups (deployment_id, server_id, status, created_by)
		VALUES ($1, $2, 'pending', $3)
		RETURNING id
	`, deploymentID, serverID, createdBy).Scan(&id)
	return id, err
}

// GetBackup looks up a single backup by ID. Returns ErrNotFound if it
// doesn't exist.
func (s *Store) GetBackup(ctx context.Context, id string) (*Backup, error) {
	var b Backup
	err := s.pool.QueryRow(ctx, `
		SELECT id, deployment_id, server_id, status, coalesce(message, ''), size_bytes, created_at, completed_at
		FROM backups WHERE id = $1
	`, id).Scan(&b.ID, &b.DeploymentID, &b.ServerID, &b.Status, &b.Message, &b.SizeBytes, &b.CreatedAt, &b.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBackupsForDeployment returns a deployment's backups, most recent
// first.
func (s *Store) ListBackupsForDeployment(ctx context.Context, deploymentID string) ([]Backup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, deployment_id, server_id, status, coalesce(message, ''), size_bytes, created_at, completed_at
		FROM backups WHERE deployment_id = $1 ORDER BY created_at DESC
	`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backups := []Backup{}
	for rows.Next() {
		var b Backup
		if err := rows.Scan(&b.ID, &b.DeploymentID, &b.ServerID, &b.Status, &b.Message, &b.SizeBytes, &b.CreatedAt, &b.CompletedAt); err != nil {
			return nil, err
		}
		backups = append(backups, b)
	}
	return backups, rows.Err()
}

// UpdateBackupStatus records progress/completion for a backup: phase and
// an optional human-readable message, always. storagePath/sizeBytes are
// only set (non-nil) once the blob upload actually lands — see
// internal/controlplane/api's blob upload handler.
func (s *Store) UpdateBackupStatus(ctx context.Context, id, status, message string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE backups
		SET status = $2, message = $3,
		    completed_at = CASE WHEN $2 IN ('completed', 'failed') THEN now() ELSE completed_at END
		WHERE id = $1
	`, id, status, message)
	return err
}

// CompleteBackupUpload records where the blob landed and how large it is,
// called by the blob-upload HTTP handler once the agent's PUT finishes
// (separate from UpdateBackupStatus's agent-reported RUNNING/FAILED
// events, which arrive over the gRPC stream on a different timeline).
func (s *Store) CompleteBackupUpload(ctx context.Context, id, storagePath string, sizeBytes int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE backups
		SET status = 'completed', storage_path = $2, size_bytes = $3, completed_at = now()
		WHERE id = $1
	`, id, storagePath, sizeBytes)
	return err
}

// GetBackupStoragePath returns where a completed backup's blob lives on
// disk, for the restore-download handler. Returns ErrNotFound if the
// backup doesn't exist or has no stored blob yet.
func (s *Store) GetBackupStoragePath(ctx context.Context, id string) (string, error) {
	var path *string
	err := s.pool.QueryRow(ctx, `SELECT storage_path FROM backups WHERE id = $1`, id).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) || path == nil {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return *path, nil
}
