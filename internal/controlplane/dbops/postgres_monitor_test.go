package dbops

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresOverviewMetricPrefersPrimaryLag(t *testing.T) {
	primary, standby := 4.0, 90.0
	sampled := time.Now().UTC()
	o := PostgresOverview{
		SampledAt: sampled, ConnectionCount: 7, TransactionsTotal: 1200, CacheHitRatio: 0.97,
		DeadlocksTotal: 3, LockWaitCount: 2, DatabaseBytes: 4096, ActiveQueryCount: 5,
		LongRunningTransactionCount: 1, ReplicationLagSeconds: &primary, StandbyReplayLagSeconds: &standby,
	}
	m := o.Metric("db1")
	if m.DatabaseID != "db1" || !m.RecordedAt.Equal(sampled) || m.ConnectionCount != 7 || m.TransactionsTotal != 1200 ||
		m.CacheHitRatio != 0.97 || m.DeadlocksTotal != 3 || m.LockWaitCount != 2 || m.DatabaseBytes != 4096 ||
		m.ActiveQueryCount != 5 || m.LongRunningTransactionCount != 1 {
		t.Errorf("Metric() = %+v", m)
	}
	if m.ReplicationLagSeconds == nil || *m.ReplicationLagSeconds != primary {
		t.Errorf("lag = %v, want primary's %v", m.ReplicationLagSeconds, primary)
	}

	o.ReplicationLagSeconds = nil
	if m = o.Metric("db1"); m.ReplicationLagSeconds == nil || *m.ReplicationLagSeconds != standby {
		t.Errorf("standby lag not used as fallback: %v", m.ReplicationLagSeconds)
	}
	o.StandbyReplayLagSeconds = nil
	if m = o.Metric("db1"); m.ReplicationLagSeconds != nil {
		t.Errorf("lag = %v, want nil with no replication", *m.ReplicationLagSeconds)
	}
}

// PostgresOverviewSQL must run on a stock server (no extensions) and decode
// into PostgresOverview. Needs TEST_DATABASE_URL.
func TestPostgresOverviewSQLDecodes(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping PostgreSQL SQL integration test")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	var raw string
	if err := conn.QueryRow(ctx, "SELECT ("+PostgresOverviewSQL+")::text").Scan(&raw); err != nil {
		t.Fatalf("overview query failed: %v", err)
	}
	var o PostgresOverview
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatalf("overview does not decode: %v\n%s", err, raw)
	}
	if o.SampledAt.IsZero() || o.ServerVersion == "" || o.ConnectionCount < 1 || o.DatabaseBytes <= 0 {
		t.Errorf("implausible overview: %+v", o)
	}
	if o.CacheHitRatio < 0 || o.CacheHitRatio > 1 {
		t.Errorf("cache hit ratio %v out of range", o.CacheHitRatio)
	}
}
