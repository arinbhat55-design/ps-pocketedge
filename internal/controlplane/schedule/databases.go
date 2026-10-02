package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/backup"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbcatalog"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbops"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/vault"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// retentionInterval is how often backup retention is enforced — pruning
// isn't time-critical, and each pass lists every instance's backups.
const retentionInterval = 10 * time.Minute

// DatabaseScheduler runs the Database Marketplace's recurring work:
// scheduled backups, backup retention, and dropping temporary database
// users once they expire.
type DatabaseScheduler struct {
	log        *slog.Logger
	st         *store.Store
	dispatcher *deploy.Dispatcher
	ops        *dbops.Ops
	blobs      *backup.BlobStore
	publicURL  string
	vault      *vault.Vault
	now        func() time.Time
}

func NewDatabaseScheduler(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, ops *dbops.Ops, blobs *backup.BlobStore, publicURL string, v *vault.Vault) *DatabaseScheduler {
	return &DatabaseScheduler{log: log, st: st, dispatcher: dispatcher, ops: ops, blobs: blobs, publicURL: publicURL, vault: v, now: time.Now}
}

// Run blocks until ctx is cancelled.
func (s *DatabaseScheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	lastRetention := time.Time{}
	lastMonitoring := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runPendingClones(ctx)
			s.runDueBackups(ctx)
			s.expireTemporaryUsers(ctx)
			if s.now().Sub(lastMonitoring) >= 2*time.Minute {
				s.samplePostgres(ctx)
				lastMonitoring = s.now()
			}
			if s.now().Sub(lastRetention) >= retentionInterval {
				s.enforceRetention(ctx)
				lastRetention = s.now()
			}
		}
	}
}

// cloneRestoreTimeout bounds how long a dispatched clone waits for its
// restore result. It is generous because the restore downloads the whole
// source backup before redeploying.
const cloneRestoreTimeout = 6 * time.Hour

// runPendingClones waits for the new instance to finish its governed
// deployment, then restores its selected consistent source backup. The job
// persists across control-plane restarts and approval delays.
func (s *DatabaseScheduler) runPendingClones(ctx context.Context) {
	if n, err := s.st.FailStaleDatabaseCloneJobs(ctx, s.now().Add(-cloneRestoreTimeout)); err != nil {
		s.log.Error("failed to expire stalled database clones", "error", err)
	} else if n > 0 {
		s.log.Warn("expired stalled database clones", "count", n)
	}
	jobs, err := s.st.PendingDatabaseCloneJobs(ctx)
	if err != nil {
		s.log.Error("failed to load pending database clones", "error", err)
		return
	}
	for _, job := range jobs {
		target, err := s.st.GetDatabaseInstance(ctx, job.TargetDatabaseID)
		if err != nil {
			s.log.Error("failed to load clone target", "database_id", job.TargetDatabaseID, "error", err)
			continue
		}
		if target.Phase == "failed" || target.Phase == "removed" {
			_ = s.st.SetDatabaseCloneJobStatus(ctx, target.ID, "failed", "target deployment failed before cloning")
			continue
		}
		if target.Phase != "running" {
			continue
		}
		b, err := s.st.GetBackup(ctx, job.BackupID)
		if err != nil || b.Status != "completed" || b.Format != "volumes" || !b.Quiesced {
			_ = s.st.SetDatabaseCloneJobStatus(ctx, target.ID, "failed", "source backup is no longer available")
			continue
		}
		source, err := s.st.GetDatabaseInstanceByDeployment(ctx, b.DeploymentID)
		if err != nil || source.Engine != "postgresql" || source.AdminUsername != target.AdminUsername || source.DatabaseName != target.DatabaseName {
			_ = s.st.SetDatabaseCloneJobStatus(ctx, target.ID, "failed", "clone source no longer matches target")
			continue
		}
		sourceMajor, _ := strconv.Atoi(strings.Split(source.Version, ".")[0])
		targetMajor, _ := strconv.Atoi(strings.Split(target.Version, ".")[0])
		if sourceMajor != targetMajor {
			_ = s.st.SetDatabaseCloneJobStatus(ctx, target.ID, "failed", "PostgreSQL major versions no longer match")
			continue
		}
		dep, err := s.st.GetDeployment(ctx, target.DeploymentID)
		if err != nil {
			s.log.Error("failed to load clone deployment", "database_id", target.ID, "error", err)
			continue
		}
		name, composeYAML, err := s.st.ResolveDeploymentSource(ctx, dep)
		if err != nil {
			s.log.Error("failed to resolve clone deployment", "database_id", target.ID, "error", err)
			continue
		}
		env, err := s.vault.ResolveEnv(ctx, dep.Env)
		if err != nil {
			s.log.Error("failed to resolve clone credentials", "database_id", target.ID, "error", err)
			continue
		}
		if env["DB_PASSWORD"] == "" {
			_ = s.st.SetDatabaseCloneJobStatus(ctx, target.ID, "failed", "target administrator password is missing")
			continue
		}
		url := s.publicURL + "/api/agent/backups/" + b.ID + "/blob"
		if b.ServerID != target.ServerID {
			if err := s.st.GrantBackupRestore(ctx, b.ID, target.ServerID, s.now().Add(24*time.Hour)); err != nil {
				s.log.Error("failed to grant clone backup transfer", "database_id", target.ID, "error", err)
				continue
			}
			url += "?serverId=" + target.ServerID
		}
		if err := s.st.SetDatabaseCloneJobStatus(ctx, target.ID, "dispatched", "restoring source backup"); err != nil {
			s.log.Error("failed to mark clone dispatched", "database_id", target.ID, "error", err)
			continue
		}
		cmd := &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_Restore{Restore: &agentv1.RestoreCommand{
			BackupId: b.ID, DeploymentId: target.DeploymentID, DownloadUrl: url, StackName: name, ComposeYaml: composeYAML, Env: env,
			SourceDeploymentId: source.DeploymentID, SyncPostgresPassword: true, PostgresUsername: target.AdminUsername, PostgresDatabase: target.DatabaseName,
		}}}
		if err := s.dispatcher.Send(target.ServerID, cmd); err != nil {
			_ = s.st.SetDatabaseCloneJobStatus(ctx, target.ID, "pending", "target server is offline; will retry")
			s.log.Warn("clone dispatch deferred", "database_id", target.ID, "error", err)
		}
	}
}

func (s *DatabaseScheduler) samplePostgres(ctx context.Context) {
	instances, err := s.st.ListDatabaseInstances(ctx)
	if err != nil {
		s.log.Error("failed to list databases for monitoring", "error", err)
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := range instances {
		inst := instances[i]
		if inst.Engine != "postgresql" || inst.Phase != "running" {
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-sem }(); s.sampleOnePostgres(ctx, &inst) }()
	}
	wg.Wait()
}

func (s *DatabaseScheduler) sampleOnePostgres(ctx context.Context, inst *store.DatabaseInstance) {
	overview, err := s.ops.PostgresOverview(ctx, inst)
	if err != nil {
		s.log.Warn("failed to sample PostgreSQL", "database_id", inst.ID, "error", err)
		return
	}
	previous, err := s.st.LatestDatabaseMetricSample(ctx, inst.ID)
	if err != nil {
		s.log.Error("failed to read previous database metric", "database_id", inst.ID, "error", err)
		return
	}
	if err := s.st.InsertDatabaseMetricSample(ctx, overview.Metric(inst.ID)); err != nil {
		s.log.Error("failed to record database metric", "database_id", inst.ID, "error", err)
		return
	}
	alerts := []struct {
		kind, severity, message string
		active                  bool
	}{
		{"long_transaction", "warning", fmt.Sprintf("%d transactions have run for more than 5 minutes", overview.LongRunningTransactionCount), overview.LongRunningTransactionCount > 0},
		{"slow_query", "warning", fmt.Sprintf("%d active queries have run for more than 30 seconds", overview.SlowQueryCount), overview.SlowQueryCount > 0},
		{"lock_wait", "warning", fmt.Sprintf("%d sessions are waiting on a lock", overview.LockWaitCount), overview.LockWaitCount > 0},
	}
	lag := overview.ReplicationLagSeconds
	if lag == nil {
		lag = overview.StandbyReplayLagSeconds
	}
	alerts = append(alerts, struct {
		kind, severity, message string
		active                  bool
	}{"replication_lag", "warning", fmt.Sprintf("replication lag is above 30 seconds"), lag != nil && *lag > 30})
	if previous != nil {
		alerts = append(alerts, struct {
			kind, severity, message string
			active                  bool
		}{"deadlock", "warning", "deadlock count increased since the last sample", overview.DeadlocksTotal > previous.DeadlocksTotal})
		growth := overview.DatabaseBytes - previous.DatabaseBytes
		alerts = append(alerts, struct {
			kind, severity, message string
			active                  bool
		}{"storage_growth", "warning", fmt.Sprintf("database grew by %d bytes since the last sample", growth), previous.DatabaseBytes > 0 && growth > 1<<30})
	}
	for _, a := range alerts {
		if err := s.st.SetDatabaseAlert(ctx, inst.ID, a.kind, a.severity, a.message, a.active); err != nil {
			s.log.Error("failed to update database alert", "database_id", inst.ID, "kind", a.kind, "error", err)
		}
	}
}

func (s *DatabaseScheduler) runDueBackups(ctx context.Context) {
	due, err := s.st.DueDatabaseBackups(ctx, s.now())
	if err != nil {
		s.log.Error("failed to load due database backups", "error", err)
		return
	}
	for i := range due {
		inst := &due[i]
		status := "dispatched"
		dep, err := s.st.GetDeployment(ctx, inst.DeploymentID)
		if err == nil {
			_, err = backup.Start(ctx, s.log, s.st, s.dispatcher, s.publicURL, backup.Request{
				Deployment: dep, Origin: "scheduled", Quiesce: inst.BackupConsistent,
			})
		}
		if err != nil {
			status = "failed: " + err.Error()
			s.log.Warn("scheduled database backup failed", "database_id", inst.ID, "error", err)
		} else {
			s.log.Info("scheduled database backup dispatched", "database_id", inst.ID)
		}

		// Always advance: a server that's offline now gets its next
		// scheduled slot, not a burst of catch-up backups when it
		// reconnects.
		var next *time.Time
		if inst.BackupCron != nil {
			if sched, err := cron.ParseStandard(*inst.BackupCron); err == nil {
				n := sched.Next(s.now().UTC()) // schedules are UTC
				next = &n
			}
		}
		if err := s.st.RecordDatabaseBackupRun(ctx, inst.ID, status, next); err != nil {
			s.log.Error("failed to record database backup run", "database_id", inst.ID, "error", err)
		}
	}
}

func (s *DatabaseScheduler) enforceRetention(ctx context.Context) {
	instances, err := s.st.ListDatabaseInstancesWithRetention(ctx)
	if err != nil {
		s.log.Error("failed to load database retention policies", "error", err)
		return
	}
	for _, inst := range instances {
		expired, err := s.st.ExpiredBackups(ctx, inst.DeploymentID, inst.RetentionDays, inst.RetentionCount, s.now())
		if err != nil {
			s.log.Error("failed to list expired backups", "database_id", inst.ID, "error", err)
			continue
		}
		for _, b := range expired {
			if b.StoragePath != nil {
				if err := s.blobs.Remove(*b.StoragePath); err != nil {
					// Keep the row so the blob isn't orphaned untracked;
					// retry next pass.
					s.log.Error("failed to delete expired backup blob", "backup_id", b.ID, "error", err)
					continue
				}
			}
			if err := s.st.DeleteBackup(ctx, b.ID); err != nil {
				s.log.Error("failed to delete expired backup", "backup_id", b.ID, "error", err)
				continue
			}
			s.log.Info("pruned backup past retention", "database_id", inst.ID, "backup_id", b.ID)
		}
	}
}

// expireTemporaryUsers drops the database users behind expired temporary
// credentials. A server that's unreachable is retried next tick — the
// credential stays unrevoked (and visibly expired) until the user is
// actually gone, so nothing claims it was removed when it wasn't.
func (s *DatabaseScheduler) expireTemporaryUsers(ctx context.Context) {
	expired, err := s.st.ExpiredTemporarySecrets(ctx, s.now())
	if err != nil {
		s.log.Error("failed to load expired temporary credentials", "error", err)
		return
	}
	for i := range expired {
		sec := &expired[i]
		if sec.DatabaseID == nil {
			_ = s.st.RevokeSecret(ctx, sec.ID)
			continue
		}
		inst, err := s.st.GetDatabaseInstance(ctx, *sec.DatabaseID)
		if errors.Is(err, store.ErrNotFound) {
			_ = s.st.RevokeSecret(ctx, sec.ID)
			continue
		}
		if err != nil {
			s.log.Error("failed to load database for expired credential", "secret_id", sec.ID, "error", err)
			continue
		}
		engine, ok := dbcatalog.Get(inst.Engine)
		if !ok {
			continue
		}
		if err := s.ops.DropTemporaryUser(ctx, inst, engine, sec); err != nil {
			s.log.Warn("failed to drop expired temporary database user; will retry", "database_id", inst.ID, "username", sec.Username, "error", err)
			continue
		}
		s.log.Info("dropped expired temporary database user", "database_id", inst.ID, "username", sec.Username)
	}
}
