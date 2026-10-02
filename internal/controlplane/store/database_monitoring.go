package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type DatabaseMetricSample struct {
	DatabaseID                  string    `json:"databaseId"`
	RecordedAt                  time.Time `json:"recordedAt"`
	ConnectionCount             int       `json:"connectionCount"`
	TransactionsTotal           int64     `json:"transactionsTotal"`
	CacheHitRatio               float64   `json:"cacheHitRatio"`
	DeadlocksTotal              int64     `json:"deadlocksTotal"`
	LockWaitCount               int       `json:"lockWaitCount"`
	DatabaseBytes               int64     `json:"databaseBytes"`
	ActiveQueryCount            int       `json:"activeQueryCount"`
	LongRunningTransactionCount int       `json:"longRunningTransactionCount"`
	ReplicationLagSeconds       *float64  `json:"replicationLagSeconds,omitempty"`
}

const databaseMetricColumns = `database_id, recorded_at, connection_count, transactions_total, cache_hit_ratio,
	deadlocks_total, lock_wait_count, database_bytes, active_query_count, long_running_transaction_count, replication_lag_seconds`

func scanDatabaseMetric(row rowScanner) (DatabaseMetricSample, error) {
	var m DatabaseMetricSample
	err := row.Scan(&m.DatabaseID, &m.RecordedAt, &m.ConnectionCount, &m.TransactionsTotal, &m.CacheHitRatio,
		&m.DeadlocksTotal, &m.LockWaitCount, &m.DatabaseBytes, &m.ActiveQueryCount, &m.LongRunningTransactionCount, &m.ReplicationLagSeconds)
	return m, err
}

func (s *Store) InsertDatabaseMetricSample(ctx context.Context, m DatabaseMetricSample) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO database_metric_samples (`+databaseMetricColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, m.DatabaseID, m.RecordedAt, m.ConnectionCount, m.TransactionsTotal, m.CacheHitRatio,
		m.DeadlocksTotal, m.LockWaitCount, m.DatabaseBytes, m.ActiveQueryCount, m.LongRunningTransactionCount, m.ReplicationLagSeconds)
	return err
}

func (s *Store) ListDatabaseMetricSamples(ctx context.Context, id string, since time.Time) ([]DatabaseMetricSample, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+databaseMetricColumns+` FROM database_metric_samples
		WHERE database_id=$1 AND recorded_at >= $2 ORDER BY recorded_at`, id, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DatabaseMetricSample{}
	for rows.Next() {
		m, err := scanDatabaseMetric(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) LatestDatabaseMetricSample(ctx context.Context, id string) (*DatabaseMetricSample, error) {
	m, err := scanDatabaseMetric(s.pool.QueryRow(ctx, `SELECT `+databaseMetricColumns+` FROM database_metric_samples WHERE database_id=$1 ORDER BY recorded_at DESC LIMIT 1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) PruneDatabaseMetricSamplesOlderThan(ctx context.Context, cutoff time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM database_metric_samples WHERE recorded_at < $1`, cutoff)
	return err
}

type DatabaseAlert struct {
	ID          int64      `json:"id"`
	DatabaseID  string     `json:"databaseId"`
	Kind        string     `json:"kind"`
	Severity    string     `json:"severity"`
	Message     string     `json:"message"`
	FirstSeenAt time.Time  `json:"firstSeenAt"`
	LastSeenAt  time.Time  `json:"lastSeenAt"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
}

func (s *Store) SetDatabaseAlert(ctx context.Context, id, kind, severity, message string, active bool) error {
	if !active {
		_, err := s.pool.Exec(ctx, `UPDATE database_alerts SET resolved_at=now() WHERE database_id=$1 AND kind=$2 AND resolved_at IS NULL`, id, kind)
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO database_alerts (database_id,kind,severity,message)
		VALUES ($1,$2,$3,$4) ON CONFLICT (database_id,kind) WHERE resolved_at IS NULL
		DO UPDATE SET severity=EXCLUDED.severity,message=EXCLUDED.message,last_seen_at=now()`, id, kind, severity, message)
	return err
}

func (s *Store) ListDatabaseAlerts(ctx context.Context, id string, includeResolved bool) ([]DatabaseAlert, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,database_id,kind,severity,message,first_seen_at,last_seen_at,resolved_at
		FROM database_alerts WHERE database_id=$1 AND ($2 OR resolved_at IS NULL)
		ORDER BY first_seen_at DESC LIMIT 100`, id, includeResolved)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DatabaseAlert{}
	for rows.Next() {
		var a DatabaseAlert
		if err := rows.Scan(&a.ID, &a.DatabaseID, &a.Kind, &a.Severity, &a.Message, &a.FirstSeenAt, &a.LastSeenAt, &a.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
