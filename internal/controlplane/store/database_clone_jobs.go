package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type DatabaseCloneJob struct {
	TargetDatabaseID string `json:"targetDatabaseId"`
	BackupID         string `json:"backupId"`
	Status           string `json:"status"`
	Message          string `json:"message"`
}

func (s *Store) CreateDatabaseCloneJob(ctx context.Context, targetID, backupID string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO database_clone_jobs (target_database_id,backup_id) VALUES ($1,$2)`, targetID, backupID)
	return err
}

func (s *Store) PendingDatabaseCloneJobs(ctx context.Context) ([]DatabaseCloneJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT target_database_id,backup_id,status,message FROM database_clone_jobs WHERE status='pending' ORDER BY created_at LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DatabaseCloneJob{}
	for rows.Next() {
		var job DatabaseCloneJob
		if err := rows.Scan(&job.TargetDatabaseID, &job.BackupID, &job.Status, &job.Message); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) GetDatabaseCloneJob(ctx context.Context, targetID string) (*DatabaseCloneJob, error) {
	var job DatabaseCloneJob
	err := s.pool.QueryRow(ctx, `SELECT target_database_id,backup_id,status,message FROM database_clone_jobs WHERE target_database_id=$1`, targetID).Scan(&job.TargetDatabaseID, &job.BackupID, &job.Status, &job.Message)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Store) SetDatabaseCloneJobStatus(ctx context.Context, targetID, status, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE database_clone_jobs SET status=$2,message=$3,updated_at=now() WHERE target_database_id=$1`, targetID, status, message)
	return err
}

// SetDatabaseCloneJobStatusByRestore records a restore result for the clone
// that dispatched it. A job already failed by FailStaleDatabaseCloneJobs is
// still settled, so a slow restore that eventually finishes is not left
// reported as failed.
func (s *Store) SetDatabaseCloneJobStatusByRestore(ctx context.Context, deploymentID, backupID, status, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE database_clone_jobs j SET status=$3,message=$4,updated_at=now()
		FROM database_instances d WHERE j.target_database_id=d.id AND d.deployment_id=$1 AND j.backup_id=$2
		AND (j.status='dispatched' OR (j.status='failed' AND j.message=$5))`, deploymentID, backupID, status, message, CloneRestoreTimedOutMessage)
	return err
}

// CloneRestoreTimedOutMessage marks a clone failed because its agent never
// reported a restore result (for example, it restarted mid-restore).
const CloneRestoreTimedOutMessage = "no restore result from the server's agent; remove this instance and create the clone again"

// FailStaleDatabaseCloneJobs fails clones dispatched before cutoff that
// never got a restore result, so they stop showing as in progress and stop
// pinning their source backup against retention.
func (s *Store) FailStaleDatabaseCloneJobs(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE database_clone_jobs SET status='failed',message=$2,updated_at=now()
		WHERE status='dispatched' AND updated_at < $1`, cutoff, CloneRestoreTimedOutMessage)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
