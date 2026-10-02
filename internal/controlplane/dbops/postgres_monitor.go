package dbops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// PostgresOverviewSQL uses cumulative PostgreSQL counters. The scheduler
// samples them so the UI can derive rates and storage growth over time.
const PostgresOverviewSQL = `SELECT jsonb_build_object(
 'sampledAt', now(),
 'serverVersion', current_setting('server_version'),
 'connectionCount', (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()),
 'connectionLimit', (SELECT datconnlimit FROM pg_database WHERE datname=current_database()),
 'transactionsTotal', (SELECT xact_commit+xact_rollback FROM pg_stat_database WHERE datname=current_database()),
 'cacheHitRatio', (SELECT CASE WHEN blks_hit+blks_read=0 THEN 1 ELSE round(blks_hit::numeric/(blks_hit+blks_read),4) END FROM pg_stat_database WHERE datname=current_database()),
 'deadlocksTotal', (SELECT deadlocks FROM pg_stat_database WHERE datname=current_database()),
 'lockWaitCount', (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'),
 'databaseBytes', pg_database_size(current_database()),
 'activeQueryCount', (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND state='active' AND pid<>pg_backend_pid()),
 'activeQueryLatencyMs', (SELECT round(avg(extract(epoch FROM now()-query_start)*1000)::numeric,1) FROM pg_stat_activity WHERE datname=current_database() AND state='active' AND pid<>pg_backend_pid()),
 'slowQueryCount', (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND state='active' AND query_start<now()-interval '30 seconds' AND pid<>pg_backend_pid()),
 'longRunningTransactionCount', (SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND xact_start<now()-interval '5 minutes' AND pid<>pg_backend_pid()),
 'replicationLagSeconds', (SELECT max(extract(epoch FROM replay_lag)) FROM pg_stat_replication),
 'standbyReplayLagSeconds', CASE WHEN pg_is_in_recovery() THEN extract(epoch FROM now()-pg_last_xact_replay_timestamp()) ELSE NULL END,
 'statsReset', (SELECT stats_reset FROM pg_stat_database WHERE datname=current_database())
)`

type PostgresOverview struct {
	SampledAt                   time.Time  `json:"sampledAt"`
	ServerVersion               string     `json:"serverVersion"`
	ConnectionCount             int        `json:"connectionCount"`
	ConnectionLimit             int        `json:"connectionLimit"`
	TransactionsTotal           int64      `json:"transactionsTotal"`
	CacheHitRatio               float64    `json:"cacheHitRatio"`
	DeadlocksTotal              int64      `json:"deadlocksTotal"`
	LockWaitCount               int        `json:"lockWaitCount"`
	DatabaseBytes               int64      `json:"databaseBytes"`
	ActiveQueryCount            int        `json:"activeQueryCount"`
	ActiveQueryLatencyMs        *float64   `json:"activeQueryLatencyMs"`
	SlowQueryCount              int        `json:"slowQueryCount"`
	LongRunningTransactionCount int        `json:"longRunningTransactionCount"`
	ReplicationLagSeconds       *float64   `json:"replicationLagSeconds"`
	StandbyReplayLagSeconds     *float64   `json:"standbyReplayLagSeconds"`
	StatsReset                  *time.Time `json:"statsReset"`
}

func (o PostgresOverview) Metric(databaseID string) store.DatabaseMetricSample {
	lag := o.ReplicationLagSeconds
	if lag == nil {
		lag = o.StandbyReplayLagSeconds
	}
	return store.DatabaseMetricSample{DatabaseID: databaseID, RecordedAt: o.SampledAt, ConnectionCount: o.ConnectionCount,
		TransactionsTotal: o.TransactionsTotal, CacheHitRatio: o.CacheHitRatio, DeadlocksTotal: o.DeadlocksTotal,
		LockWaitCount: o.LockWaitCount, DatabaseBytes: o.DatabaseBytes, ActiveQueryCount: o.ActiveQueryCount,
		LongRunningTransactionCount: o.LongRunningTransactionCount, ReplicationLagSeconds: lag}
}

func (o *Ops) PostgresOverview(ctx context.Context, inst *store.DatabaseInstance) (*PostgresOverview, error) {
	if inst.Engine != "postgresql" {
		return nil, fmt.Errorf("PostgreSQL overview unsupported for %s", inst.Engine)
	}
	database := inst.DatabaseName
	if database == "" {
		database = "postgres"
	}
	argv := []string{"psql", "-X", "-v", "ON_ERROR_STOP=1", "-A", "-t", "-U", inst.AdminUsername, "-d", database, "-c", PostgresOverviewSQL}
	res, err := deploy.RunCommandWithLimit(ctx, o.dispatcher, o.relay, inst.ServerID, PrimaryContainer(inst), argv, 30*time.Second, 1<<20)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("PostgreSQL overview failed: %s", strings.TrimSpace(res.Output))
	}
	if res.Truncated {
		return nil, fmt.Errorf("PostgreSQL overview output exceeded 1 MB")
	}
	var overview PostgresOverview
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.ReplaceAll(res.Output, "\r", ""))), &overview); err != nil {
		return nil, err
	}
	return &overview, nil
}
