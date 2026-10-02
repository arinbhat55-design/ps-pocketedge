package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Integration tests for migrations 0018-0023: managed secrets, database
// monitoring, backup consistency/format, cross-server restore grants and
// clone jobs. Like the rest of this package they need TEST_DATABASE_URL.

func TestBackupConsistencyAndFormatRoundTrip(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	inst, _ := createTestDatabase(t, st)

	volumeID, err := st.CreateBackup(ctx, inst.DeploymentID, inst.ServerID, "", "manual", true, "volumes")
	if err != nil {
		t.Fatalf("CreateBackup(volumes): %v", err)
	}
	logicalID, err := st.CreateBackup(ctx, inst.DeploymentID, inst.ServerID, "", "scheduled", false, "postgres_custom")
	if err != nil {
		t.Fatalf("CreateBackup(postgres_custom): %v", err)
	}

	b, err := st.GetBackup(ctx, volumeID)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Quiesced || b.Format != "volumes" || b.Origin != "manual" || b.Status != "pending" {
		t.Errorf("volume backup = %+v", b)
	}
	b, err = st.GetBackup(ctx, logicalID)
	if err != nil {
		t.Fatal(err)
	}
	if b.Quiesced || b.Format != "postgres_custom" || b.Origin != "scheduled" {
		t.Errorf("logical backup = %+v", b)
	}

	list, err := st.ListBackupsForDeployment(ctx, inst.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	formats := map[string]string{}
	for _, b := range list {
		formats[b.ID] = b.Format
	}
	if formats[volumeID] != "volumes" || formats[logicalID] != "postgres_custom" {
		t.Errorf("ListBackupsForDeployment formats = %v", formats)
	}

	if _, err := st.CreateBackup(ctx, inst.DeploymentID, inst.ServerID, "", "manual", false, "tarball"); err == nil {
		t.Error("CreateBackup accepted a format outside the CHECK constraint")
	}
}

// Rows written before 0020/0021 must read back as ordinary, non-quiesced
// volume backups so they are never offered for refresh or clone.
func TestBackupMigrationDefaults(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	inst, _ := createTestDatabase(t, st)

	var id string
	if err := st.pool.QueryRow(ctx, `INSERT INTO backups (deployment_id, server_id, status) VALUES ($1, $2, 'completed') RETURNING id`,
		inst.DeploymentID, inst.ServerID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	b, err := st.GetBackup(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if b.Quiesced || b.Format != "volumes" {
		t.Errorf("legacy backup defaults = quiesced %v, format %q", b.Quiesced, b.Format)
	}
}

func TestBackupRestoreGrants(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	inst, _ := createTestDatabase(t, st)
	otherServer := createTestServer(t, st)

	backupID, err := st.CreateBackup(ctx, inst.DeploymentID, inst.ServerID, "", "manual", true, "volumes")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := st.HasBackupRestoreGrant(ctx, backupID, otherServer); err != nil || ok {
		t.Fatalf("grant before GrantBackupRestore = %v, %v", ok, err)
	}

	if err := st.GrantBackupRestore(ctx, backupID, otherServer, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.HasBackupRestoreGrant(ctx, backupID, otherServer); ok {
		t.Error("expired grant reported as valid")
	}

	// Re-granting upserts the expiry rather than failing on the primary key.
	if err := st.GrantBackupRestore(ctx, backupID, otherServer, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	if ok, _ := st.HasBackupRestoreGrant(ctx, backupID, otherServer); !ok {
		t.Error("renewed grant not reported as valid")
	}
	if ok, _ := st.HasBackupRestoreGrant(ctx, backupID, inst.ServerID); ok {
		t.Error("grant leaked to a server it was not issued for")
	}
}

func TestDatabaseCloneJobLifecycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	source, _ := createTestDatabase(t, st)
	target, _ := createTestDatabase(t, st)

	if job, err := st.GetDatabaseCloneJob(ctx, target.ID); err != nil || job != nil {
		t.Fatalf("GetDatabaseCloneJob with no job = %+v, %v", job, err)
	}

	backupID, err := st.CreateBackup(ctx, source.DeploymentID, source.ServerID, "", "manual", true, "volumes")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDatabaseCloneJob(ctx, target.ID, backupID); err != nil {
		t.Fatalf("CreateDatabaseCloneJob: %v", err)
	}
	job, err := st.GetDatabaseCloneJob(ctx, target.ID)
	if err != nil || job == nil || job.Status != "pending" || job.BackupID != backupID {
		t.Fatalf("new clone job = %+v, %v", job, err)
	}
	if !containsCloneJob(t, st, target.ID) {
		t.Error("pending clone job not listed by PendingDatabaseCloneJobs")
	}

	// A restore result only settles a job that has actually been dispatched.
	if err := st.SetDatabaseCloneJobStatusByRestore(ctx, target.DeploymentID, backupID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if job, _ = st.GetDatabaseCloneJob(ctx, target.ID); job.Status != "pending" {
		t.Errorf("restore result settled an undispatched job: %q", job.Status)
	}

	if err := st.SetDatabaseCloneJobStatus(ctx, target.ID, "dispatched", "restoring source backup"); err != nil {
		t.Fatal(err)
	}
	if containsCloneJob(t, st, target.ID) {
		t.Error("dispatched job still listed as pending")
	}

	// A restore of some other backup into the same deployment (e.g. a plain
	// restore) must not complete the clone.
	otherBackup, err := st.CreateBackup(ctx, target.DeploymentID, target.ServerID, "", "manual", true, "volumes")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetDatabaseCloneJobStatusByRestore(ctx, target.DeploymentID, otherBackup, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if job, _ = st.GetDatabaseCloneJob(ctx, target.ID); job.Status != "dispatched" {
		t.Errorf("unrelated restore changed clone status to %q", job.Status)
	}

	if err := st.SetDatabaseCloneJobStatusByRestore(ctx, target.DeploymentID, backupID, "failed", "disk full"); err != nil {
		t.Fatal(err)
	}
	if job, _ = st.GetDatabaseCloneJob(ctx, target.ID); job.Status != "failed" || job.Message != "disk full" {
		t.Errorf("clone after failed restore = %+v", job)
	}

	if err := st.SetDatabaseCloneJobStatus(ctx, target.ID, "bogus", ""); err == nil {
		t.Error("clone job accepted a status outside the CHECK constraint")
	}
}

func containsCloneJob(t *testing.T, st *Store, targetID string) bool {
	t.Helper()
	jobs, err := st.PendingDatabaseCloneJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.TargetDatabaseID == targetID {
			return true
		}
	}
	return false
}

// Retention must not prune a backup an in-flight clone is about to restore.
func TestExpiredBackupsSkipsInFlightCloneSource(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	source, _ := createTestDatabase(t, st)
	target, _ := createTestDatabase(t, st)

	now := time.Now()
	add := func(age time.Duration) string {
		var id string
		if err := st.pool.QueryRow(ctx, `
			INSERT INTO backups (deployment_id, server_id, status, storage_path, created_at, quiesced)
			VALUES ($1, $2, 'completed', '/tmp/x.tar', $3, true) RETURNING id
		`, source.DeploymentID, source.ServerID, now.Add(-age)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	add(time.Hour)
	old := add(48 * time.Hour)

	expiredIDs := func() map[string]bool {
		expired, err := st.ExpiredBackups(ctx, source.DeploymentID, 1, 0, now)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, b := range expired {
			ids[b.ID] = true
		}
		return ids
	}
	if !expiredIDs()[old] {
		t.Fatal("old backup not expired before any clone references it")
	}

	if err := st.CreateDatabaseCloneJob(ctx, target.ID, old); err != nil {
		t.Fatal(err)
	}
	if expiredIDs()[old] {
		t.Error("backup referenced by a pending clone was expired")
	}
	if err := st.SetDatabaseCloneJobStatus(ctx, target.ID, "dispatched", ""); err != nil {
		t.Fatal(err)
	}
	if expiredIDs()[old] {
		t.Error("backup referenced by a dispatched clone was expired")
	}
	if err := st.SetDatabaseCloneJobStatus(ctx, target.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if !expiredIDs()[old] {
		t.Error("backup still protected after its clone completed")
	}
}

func TestUpdateDatabaseConfiguration(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	inst, _ := createTestDatabase(t, st)

	if err := st.UpdateDatabaseConfiguration(ctx, inst.ID, "17.2", 2048, 2.5, 20); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetDatabaseInstance(ctx, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "17.2" || got.MemoryMB != 2048 || got.CPUs != 2.5 || got.StorageGB != 20 {
		t.Errorf("after UpdateDatabaseConfiguration = %+v", got)
	}
	err = st.UpdateDatabaseConfiguration(ctx, "00000000-0000-0000-0000-000000000000", "17", 1, 1, 1)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateDatabaseConfiguration(missing) = %v, want ErrNotFound", err)
	}
}

func TestDeleteDatabaseInstanceCascades(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	source, _ := createTestDatabase(t, st)
	inst, secretID := createTestDatabase(t, st)

	managedID, err := st.CreateSecret(ctx, NewSecret{Name: "Managed PostgreSQL user", Kind: "managed", Username: "reporting", Ciphertext: []byte("sealed")})
	if err != nil {
		t.Fatalf("managed secret rejected (migration 0018): %v", err)
	}
	if err := st.LinkSecretsToDatabase(ctx, inst.ID, []string{managedID}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertDatabaseMetricSample(ctx, DatabaseMetricSample{DatabaseID: inst.ID, RecordedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDatabaseAlert(ctx, inst.ID, "lock_wait", "warning", "waiting", true); err != nil {
		t.Fatal(err)
	}
	backupID, err := st.CreateBackup(ctx, source.DeploymentID, source.ServerID, "", "manual", true, "volumes")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDatabaseCloneJob(ctx, inst.ID, backupID); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteDatabaseInstance(ctx, inst.ID); err != nil {
		t.Fatalf("DeleteDatabaseInstance: %v", err)
	}
	if _, err := st.GetDatabaseInstance(ctx, inst.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("instance still readable after delete: %v", err)
	}
	for table, where := range map[string]string{
		"secrets":                 "id = ANY($1::uuid[])",
		"database_metric_samples": "database_id = ($1::uuid[])[3]",
		"database_alerts":         "database_id = ($1::uuid[])[3]",
		"database_clone_jobs":     "target_database_id = ($1::uuid[])[3]",
	} {
		var n int
		if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE `+where,
			[]string{secretID, managedID, inst.ID}).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%d %s rows survived the instance delete", n, table)
		}
	}
	// The source's backup is retained; only the clone job pointing at it went.
	if _, err := st.GetBackup(ctx, backupID); err != nil {
		t.Errorf("source backup removed with the clone target: %v", err)
	}
	if err := st.DeleteDatabaseInstance(ctx, inst.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

func TestDatabaseMetricSamples(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	inst, _ := createTestDatabase(t, st)

	if latest, err := st.LatestDatabaseMetricSample(ctx, inst.ID); err != nil || latest != nil {
		t.Fatalf("latest with no samples = %+v, %v", latest, err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	lag := 12.5
	samples := []DatabaseMetricSample{
		{DatabaseID: inst.ID, RecordedAt: now.Add(-48 * time.Hour), ConnectionCount: 1},
		{DatabaseID: inst.ID, RecordedAt: now.Add(-time.Hour), ConnectionCount: 3, TransactionsTotal: 100, DatabaseBytes: 1 << 20},
		{DatabaseID: inst.ID, RecordedAt: now, ConnectionCount: 5, TransactionsTotal: 160, CacheHitRatio: 0.99,
			DeadlocksTotal: 2, LockWaitCount: 1, DatabaseBytes: 2 << 20, ActiveQueryCount: 4, LongRunningTransactionCount: 1, ReplicationLagSeconds: &lag},
	}
	for _, s := range samples {
		if err := st.InsertDatabaseMetricSample(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.ListDatabaseMetricSamples(ctx, inst.ID, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ConnectionCount != 3 || got[1].ConnectionCount != 5 {
		t.Fatalf("samples in window (oldest first) = %+v", got)
	}
	last := got[1]
	if last.CacheHitRatio != 0.99 || last.DeadlocksTotal != 2 || last.ActiveQueryCount != 4 ||
		last.ReplicationLagSeconds == nil || *last.ReplicationLagSeconds != lag {
		t.Errorf("round-tripped sample = %+v", last)
	}
	if got[0].ReplicationLagSeconds != nil {
		t.Error("nil replication lag read back as a value")
	}

	latest, err := st.LatestDatabaseMetricSample(ctx, inst.ID)
	if err != nil || latest == nil || !latest.RecordedAt.Equal(now) {
		t.Fatalf("latest = %+v, %v", latest, err)
	}

	if err := st.PruneDatabaseMetricSamplesOlderThan(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	all, _ := st.ListDatabaseMetricSamples(ctx, inst.ID, time.Time{})
	if len(all) != 2 {
		t.Errorf("after prune %d samples remain, want 2", len(all))
	}
}

func TestDatabaseAlertsOpenUpdateResolve(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	inst, _ := createTestDatabase(t, st)

	if err := st.SetDatabaseAlert(ctx, inst.ID, "slow_query", "warning", "1 slow", true); err != nil {
		t.Fatal(err)
	}
	// Re-raising an open alert updates it in place rather than duplicating.
	if err := st.SetDatabaseAlert(ctx, inst.ID, "slow_query", "warning", "3 slow", true); err != nil {
		t.Fatal(err)
	}
	// Resolving an alert that was never raised is a no-op.
	if err := st.SetDatabaseAlert(ctx, inst.ID, "deadlock", "warning", "", false); err != nil {
		t.Fatal(err)
	}
	open, err := st.ListDatabaseAlerts(ctx, inst.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].Message != "3 slow" || open[0].ResolvedAt != nil {
		t.Fatalf("open alerts = %+v", open)
	}
	firstID := open[0].ID

	if err := st.SetDatabaseAlert(ctx, inst.ID, "slow_query", "", "", false); err != nil {
		t.Fatal(err)
	}
	if open, _ = st.ListDatabaseAlerts(ctx, inst.ID, false); len(open) != 0 {
		t.Errorf("alert still open after resolve: %+v", open)
	}

	// A recurrence after resolution opens a fresh alert and keeps history.
	if err := st.SetDatabaseAlert(ctx, inst.ID, "slow_query", "warning", "again", true); err != nil {
		t.Fatal(err)
	}
	all, err := st.ListDatabaseAlerts(ctx, inst.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("alert history has %d rows, want 2", len(all))
	}
	for _, a := range all {
		if a.ID == firstID && a.ResolvedAt == nil {
			t.Error("original alert lost its resolution")
		}
		if a.ID != firstID && (a.ResolvedAt != nil || a.Message != "again") {
			t.Errorf("recurrence = %+v", a)
		}
	}
}

// A clone whose agent never reports back is failed after the timeout, and
// stops pinning its source backup; a late success still settles it, but a
// genuine restore failure is not overwritten.
func TestFailStaleDatabaseCloneJobs(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	source, _ := createTestDatabase(t, st)
	stale, _ := createTestDatabase(t, st)
	fresh, _ := createTestDatabase(t, st)

	backupID, err := st.CreateBackup(ctx, source.DeploymentID, source.ServerID, "", "manual", true, "volumes")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []*DatabaseInstance{stale, fresh} {
		if err := st.CreateDatabaseCloneJob(ctx, target.ID, backupID); err != nil {
			t.Fatal(err)
		}
		if err := st.SetDatabaseCloneJobStatus(ctx, target.ID, "dispatched", "restoring source backup"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.pool.Exec(ctx, `UPDATE database_clone_jobs SET updated_at = now() - interval '7 hours' WHERE target_database_id = $1`, stale.ID); err != nil {
		t.Fatal(err)
	}

	n, err := st.FailStaleDatabaseCloneJobs(ctx, time.Now().Add(-6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expired %d clones, want 1", n)
	}
	job, _ := st.GetDatabaseCloneJob(ctx, stale.ID)
	if job.Status != "failed" || job.Message != CloneRestoreTimedOutMessage {
		t.Errorf("stale clone = %+v", job)
	}
	if job, _ = st.GetDatabaseCloneJob(ctx, fresh.ID); job.Status != "dispatched" {
		t.Errorf("recent clone expired early: %+v", job)
	}

	// The restore finishes after all: the timed-out job is settled.
	if err := st.SetDatabaseCloneJobStatusByRestore(ctx, stale.DeploymentID, backupID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if job, _ = st.GetDatabaseCloneJob(ctx, stale.ID); job.Status != "completed" {
		t.Errorf("late restore result not applied: %+v", job)
	}

	// A real restore failure is final.
	if err := st.SetDatabaseCloneJobStatusByRestore(ctx, fresh.DeploymentID, backupID, "failed", "disk full"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDatabaseCloneJobStatusByRestore(ctx, fresh.DeploymentID, backupID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if job, _ = st.GetDatabaseCloneJob(ctx, fresh.ID); job.Status != "failed" || job.Message != "disk full" {
		t.Errorf("restore failure overwritten: %+v", job)
	}
}
