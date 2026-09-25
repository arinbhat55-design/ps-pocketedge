package schedule

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/backup"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbcatalog"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbops"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
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
	now        func() time.Time
}

func NewDatabaseScheduler(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, ops *dbops.Ops, blobs *backup.BlobStore, publicURL string) *DatabaseScheduler {
	return &DatabaseScheduler{log: log, st: st, dispatcher: dispatcher, ops: ops, blobs: blobs, publicURL: publicURL, now: time.Now}
}

// Run blocks until ctx is cancelled.
func (s *DatabaseScheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	lastRetention := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDueBackups(ctx)
			s.expireTemporaryUsers(ctx)
			if s.now().Sub(lastRetention) >= retentionInterval {
				s.enforceRetention(ctx)
				lastRetention = s.now()
			}
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
