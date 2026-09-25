package store

import (
	"context"
	"testing"
	"time"
)

// createTestDatabase inserts a server, compose file, deployment, admin
// secret and database instance, cleaning all of them up afterwards.
func createTestDatabase(t *testing.T, st *Store) (*DatabaseInstance, string) {
	t.Helper()
	ctx := context.Background()
	serverID := createTestServer(t, st)
	fileID, err := st.CreateComposeFile(ctx, "db-test-"+serverID[:8], "services: {}\n", "")
	if err != nil {
		t.Fatal(err)
	}
	secretID, err := st.CreateSecret(ctx, NewSecret{Name: "Administrator password", Kind: "admin", Username: "dbadmin", Ciphertext: []byte("sealed")})
	if err != nil {
		t.Fatal(err)
	}
	deploymentID, err := st.InsertDeployment(ctx, NewDeployment{ComposeFileID: &fileID, ServerID: serverID, Env: map[string]string{"DB_PASSWORD": "vault:" + secretID}})
	if err != nil {
		t.Fatal(err)
	}
	cronExpr := "0 3 * * *"
	next := time.Now().Add(-time.Minute)
	id, err := st.InsertDatabaseInstance(ctx, NewDatabaseInstance{
		Name: "test-" + serverID[:8], Engine: "postgresql", Version: "17", DeploymentID: deploymentID,
		ComposeFileID: fileID, ServerID: serverID, PrimaryService: "db", DatabaseName: "app",
		AdminUsername: "dbadmin", Port: 5432, Access: "local", Profile: "development",
		StorageGB: 10, MemoryMB: 1024, CPUs: 1.5, AdminSecretID: secretID,
		BackupPolicy: BackupPolicy{Cron: &cronExpr, NextRunAt: &next, Consistent: true, RetentionCount: 2},
	})
	if err != nil {
		t.Fatalf("InsertDatabaseInstance: %v", err)
	}
	if err := st.LinkSecretsToDatabase(ctx, id, []string{secretID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = st.pool.Exec(bg, `DELETE FROM backups WHERE deployment_id = $1`, deploymentID)
		_, _ = st.pool.Exec(bg, `DELETE FROM database_instances WHERE id = $1`, id)
		_, _ = st.pool.Exec(bg, `DELETE FROM secrets WHERE id = $1`, secretID)
		_, _ = st.pool.Exec(bg, `DELETE FROM deployments WHERE id = $1`, deploymentID)
		_, _ = st.pool.Exec(bg, `DELETE FROM compose_files WHERE id = $1`, fileID)
	})
	inst, err := st.GetDatabaseInstance(ctx, id)
	if err != nil {
		t.Fatalf("GetDatabaseInstance: %v", err)
	}
	return inst, secretID
}

func TestDatabaseInstanceRoundTrip(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	inst, secretID := createTestDatabase(t, st)

	if inst.CPUs != 1.5 || inst.PrimaryService != "db" || inst.AdminSecretID == nil || *inst.AdminSecretID != secretID {
		t.Fatalf("unexpected instance: %+v", inst)
	}
	if byDep, err := st.GetDatabaseInstanceByDeployment(ctx, inst.DeploymentID); err != nil || byDep.ID != inst.ID {
		t.Fatalf("GetDatabaseInstanceByDeployment = %v, %v", byDep, err)
	}
	if taken, _ := st.DatabaseNameTaken(ctx, inst.Name); !taken {
		t.Error("name not reported taken")
	}
	if name, taken, _ := st.DatabasePortTaken(ctx, inst.ServerID, 5432); !taken || name != inst.Name {
		t.Errorf("port not reported taken: %q %v", name, taken)
	}

	due, err := st.DueDatabaseBackups(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range due {
		found = found || d.ID == inst.ID
	}
	if !found {
		t.Error("instance with a past backup_next_run_at not reported due")
	}
	next := time.Now().Add(time.Hour)
	if err := st.RecordDatabaseBackupRun(ctx, inst.ID, "dispatched", &next); err != nil {
		t.Fatal(err)
	}
	due, _ = st.DueDatabaseBackups(ctx, time.Now())
	for _, d := range due {
		if d.ID == inst.ID {
			t.Error("instance still due after its run was recorded")
		}
	}

	secrets, err := st.ServerSecretCiphertexts(ctx, inst.ServerID)
	if err != nil || len(secrets) != 1 || string(secrets[0]) != "sealed" {
		t.Errorf("ServerSecretCiphertexts = %q, %v", secrets, err)
	}
}

func TestExpiredBackupsRetention(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	inst, _ := createTestDatabase(t, st)

	now := time.Now()
	add := func(age time.Duration, status string) string {
		var id string
		err := st.pool.QueryRow(ctx, `
			INSERT INTO backups (deployment_id, server_id, status, storage_path, created_at)
			VALUES ($1, $2, $3, '/tmp/x.tar', $4) RETURNING id
		`, inst.DeploymentID, inst.ServerID, status, now.Add(-age)).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	newest := add(1*time.Hour, "completed")
	second := add(2*24*time.Hour, "completed")
	add(3*24*time.Hour, "failed") // never pruned, never counted
	third := add(10*24*time.Hour, "completed")

	ids := func(list []ExpiredBackup) map[string]bool {
		m := map[string]bool{}
		for _, b := range list {
			m[b.ID] = true
		}
		return m
	}

	byCount, err := st.ExpiredBackups(ctx, inst.DeploymentID, 0, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(byCount); len(got) != 1 || !got[third] {
		t.Errorf("keep 2: expired = %v, want only the oldest completed", got)
	}

	byAge, err := st.ExpiredBackups(ctx, inst.DeploymentID, 7, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(byAge); len(got) != 1 || !got[third] {
		t.Errorf("7 days: expired = %v", got)
	}

	both, _ := st.ExpiredBackups(ctx, inst.DeploymentID, 1, 5, now)
	if got := ids(both); len(got) != 2 || !got[second] || !got[third] || got[newest] {
		t.Errorf("1 day or 5: expired = %v", got)
	}

	none, _ := st.ExpiredBackups(ctx, inst.DeploymentID, 0, 0, now)
	if len(none) != 0 {
		t.Errorf("no policy: expired = %v", none)
	}
}

func TestSecretLifecycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	inst, secretID := createTestDatabase(t, st)
	userID, err := st.CreateUser(ctx, "grantee-"+inst.ID[:8]+"@example.com", "hash", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) })

	// One-time download.
	if ok, _ := st.ClaimSecretDownload(ctx, secretID); !ok {
		t.Fatal("first download refused")
	}
	if ok, _ := st.ClaimSecretDownload(ctx, secretID); ok {
		t.Fatal("second download allowed")
	}
	// Rotation bumps the version and re-arms the download.
	if err := st.ReplaceSecretValue(ctx, secretID, []byte("sealed-2")); err != nil {
		t.Fatal(err)
	}
	sec, _ := st.GetSecret(ctx, secretID)
	if sec.Version != 2 || sec.DownloadedAt != nil || sec.RotatedAt == nil {
		t.Fatalf("after rotation: %+v", sec)
	}
	if ok, _ := st.ClaimSecretDownload(ctx, secretID); !ok {
		t.Fatal("download not re-armed by rotation")
	}

	// Grants, including expiry.
	if ok, _ := st.HasActiveSecretGrant(ctx, secretID, userID, time.Now()); ok {
		t.Fatal("grant before sharing")
	}
	expires := time.Now().Add(time.Hour)
	if err := st.UpsertSecretGrant(ctx, secretID, userID, "", &expires); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.HasActiveSecretGrant(ctx, secretID, userID, time.Now()); !ok {
		t.Fatal("grant not active")
	}
	if ok, _ := st.HasActiveSecretGrant(ctx, secretID, userID, time.Now().Add(2*time.Hour)); ok {
		t.Fatal("grant active after its expiry")
	}
	grants, _ := st.ListSecretGrants(ctx, secretID)
	if len(grants) != 1 || grants[0].UserID != userID {
		t.Fatalf("grants = %+v", grants)
	}
	if err := st.DeleteSecretGrant(ctx, secretID, userID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.HasActiveSecretGrant(ctx, secretID, userID, time.Now()); ok {
		t.Fatal("grant survived deletion")
	}

	// Temporary credential expiry.
	past := time.Now().Add(-time.Minute)
	tmpID, err := st.CreateSecret(ctx, NewSecret{Name: "tmp", Kind: "temporary", Username: "tmp_x", Ciphertext: []byte("s"), DatabaseID: inst.ID, ExpiresAt: &past})
	if err != nil {
		t.Fatal(err)
	}
	expired, _ := st.ExpiredTemporarySecrets(ctx, time.Now())
	found := false
	for _, s := range expired {
		found = found || s.ID == tmpID
	}
	if !found {
		t.Fatal("expired temporary credential not listed")
	}
	if err := st.RevokeSecret(ctx, tmpID); err != nil {
		t.Fatal(err)
	}
	expired, _ = st.ExpiredTemporarySecrets(ctx, time.Now())
	for _, s := range expired {
		if s.ID == tmpID {
			t.Fatal("revoked credential still listed as expiring")
		}
	}
	list, _ := st.ListSecretsForDatabase(ctx, inst.ID)
	if len(list) != 2 || list[0].Kind != "admin" {
		t.Fatalf("ListSecretsForDatabase = %+v", list)
	}
}
