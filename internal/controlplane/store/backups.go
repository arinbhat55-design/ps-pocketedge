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
	ID           string `json:"id"`
	DeploymentID string `json:"deploymentId"`
	ServerID     string `json:"serverId"`
	Status       string `json:"status"`
	Message      string `json:"message"`
	// Origin is "manual" or "scheduled".
	Origin      string     `json:"origin"`
	Quiesced    bool       `json:"quiesced"`
	Format      string     `json:"format"`
	SizeBytes   *int64     `json:"sizeBytes,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// CreateBackup inserts a new backup row in the 'pending' phase. createdBy
// is empty for a scheduled backup; origin is "manual" or "scheduled".
func (s *Store) CreateBackup(ctx context.Context, deploymentID, serverID, createdBy, origin string, quiesced bool, format string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO backups (deployment_id, server_id, status, created_by, origin, quiesced, format)
		VALUES ($1, $2, 'pending', $3, $4, $5, $6)
		RETURNING id
	`, deploymentID, serverID, nullableUUID(createdBy), origin, quiesced, format).Scan(&id)
	return id, err
}

// GetBackup looks up a single backup by ID. Returns ErrNotFound if it
// doesn't exist.
func (s *Store) GetBackup(ctx context.Context, id string) (*Backup, error) {
	var b Backup
	err := s.pool.QueryRow(ctx, `
		SELECT id, deployment_id, server_id, status, coalesce(message, ''), origin, quiesced, format, size_bytes, created_at, completed_at
		FROM backups WHERE id = $1
	`, id).Scan(&b.ID, &b.DeploymentID, &b.ServerID, &b.Status, &b.Message, &b.Origin, &b.Quiesced, &b.Format, &b.SizeBytes, &b.CreatedAt, &b.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) GrantBackupRestore(ctx context.Context, backupID, serverID string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO backup_restore_grants (backup_id,server_id,expires_at) VALUES ($1,$2,$3)
		ON CONFLICT (backup_id,server_id) DO UPDATE SET expires_at=EXCLUDED.expires_at`, backupID, serverID, expiresAt)
	return err
}

func (s *Store) HasBackupRestoreGrant(ctx context.Context, backupID, serverID string) (bool, error) {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM backup_restore_grants WHERE backup_id=$1 AND server_id=$2 AND expires_at>now())`, backupID, serverID).Scan(&allowed)
	return allowed, err
}

// ListBackupsForDeployment returns a deployment's backups, most recent
// first.
func (s *Store) ListBackupsForDeployment(ctx context.Context, deploymentID string) ([]Backup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, deployment_id, server_id, status, coalesce(message, ''), origin, quiesced, format, size_bytes, created_at, completed_at
		FROM backups WHERE deployment_id = $1 ORDER BY created_at DESC
	`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backups := []Backup{}
	for rows.Next() {
		var b Backup
		if err := rows.Scan(&b.ID, &b.DeploymentID, &b.ServerID, &b.Status, &b.Message, &b.Origin, &b.Quiesced, &b.Format, &b.SizeBytes, &b.CreatedAt, &b.CompletedAt); err != nil {
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
		SET status = 'completed', storage_path = $2, size_bytes = $3, completed_at = now(), message = NULL
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

// ExpiredBackup is a backup that falls outside its instance's retention
// policy, with where its blob lives (if it has one) so the caller can
// delete it.
type ExpiredBackup struct {
	ID          string
	StoragePath *string
}

// ExpiredBackups returns deploymentID's completed backups beyond the
// retention policy: older than retentionDays (0 = no age limit), or beyond
// the newest retentionCount (0 = no count limit). Only completed backups
// are considered and counted — a pending or failed one neither expires
// nor protects an older good one from pruning.
func (s *Store) ExpiredBackups(ctx context.Context, deploymentID string, retentionDays, retentionCount int, now time.Time) ([]ExpiredBackup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, storage_path FROM (
			SELECT id, storage_path, created_at,
			       row_number() OVER (ORDER BY created_at DESC) AS rank
			FROM backups WHERE deployment_id = $1 AND status = 'completed'
		) b
		WHERE (($2 > 0 AND b.created_at < $4::timestamptz - make_interval(days => $2))
		   OR ($3 > 0 AND b.rank > $3))
		  AND NOT EXISTS (SELECT 1 FROM database_clone_jobs j WHERE j.backup_id=b.id AND j.status IN ('pending','dispatched'))
	`, deploymentID, retentionDays, retentionCount, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExpiredBackup{}
	for rows.Next() {
		var e ExpiredBackup
		if err := rows.Scan(&e.ID, &e.StoragePath); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteBackup removes a backup row.
func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM backups WHERE id = $1`, id)
	return err
}
