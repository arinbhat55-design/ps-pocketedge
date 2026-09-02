package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// testStore connects to a real Postgres for integration tests — there's no
// mock/fake for pgx in this codebase, so these tests exercise actual SQL.
// Skipped unless TEST_DATABASE_URL is set (e.g. to the connection string
// deploy/docker-compose.dev.yml's postgres service exposes on :55432),
// since most environments running `go test ./...` won't have that database
// up.
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping store integration test (see deploy/docker-compose.dev.yml)")
	}
	st, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// createTestServer inserts a throwaway servers row for a test to attach
// samples to, and registers cleanup to remove it (and, via ON DELETE
// CASCADE, anything referencing it) once the test finishes.
func createTestServer(t *testing.T, st *Store) string {
	t.Helper()
	ctx := context.Background()
	id, err := st.CreateServer(ctx, "test-host", "linux", "amd64", "test", "test-token-hash", nil)
	if err != nil {
		t.Fatalf("failed to create test server: %v", err)
	}
	t.Cleanup(func() {
		if _, err := st.pool.Exec(context.Background(), `DELETE FROM servers WHERE id = $1`, id); err != nil {
			t.Logf("failed to clean up test server %s: %v", id, err)
		}
	})
	return id
}

func TestInsertAndListContainerMetricSamples(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	serverID := createTestServer(t, st)

	now := time.Now().UTC().Truncate(time.Millisecond)
	old := now.Add(-2 * time.Hour)

	// One sample outside the query window (too old) and two inside it, for
	// two different containers, to check both the since-filter and that
	// results aren't cross-contaminated between containers.
	samples := []struct {
		containerID string
		recordedAt  time.Time
		usage       ContainerResourceUsage
	}{
		{
			containerID: "container-a",
			recordedAt:  old,
			usage:       ContainerResourceUsage{ContainerID: "container-a", CPUPercent: 1},
		},
		{
			containerID: "container-a",
			recordedAt:  now,
			usage: ContainerResourceUsage{
				ContainerID:     "container-a",
				CPUPercent:      12.5,
				MemUsageBytes:   1024,
				MemLimitBytes:   2048,
				MemPercent:      50,
				NetRxBytes:      10,
				NetTxBytes:      20,
				BlockReadBytes:  30,
				BlockWriteBytes: 40,
				PIDs:            5,
			},
		},
		{
			containerID: "container-b",
			recordedAt:  now,
			usage:       ContainerResourceUsage{ContainerID: "container-b", CPUPercent: 99},
		},
	}

	for _, s := range samples {
		if err := st.InsertContainerMetricSample(ctx, serverID, s.recordedAt, s.usage); err != nil {
			t.Fatalf("InsertContainerMetricSample(%s) failed: %v", s.containerID, err)
		}
	}

	got, err := st.ListContainerMetricSamples(ctx, serverID, "container-a", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListContainerMetricSamples failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d samples, want 1 (the old one should be excluded by the since filter)", len(got))
	}

	want := samples[1].usage
	got0 := got[0]
	if got0.ContainerID != want.ContainerID ||
		got0.CPUPercent != want.CPUPercent ||
		got0.MemUsageBytes != want.MemUsageBytes ||
		got0.MemLimitBytes != want.MemLimitBytes ||
		got0.MemPercent != want.MemPercent ||
		got0.NetRxBytes != want.NetRxBytes ||
		got0.NetTxBytes != want.NetTxBytes ||
		got0.BlockReadBytes != want.BlockReadBytes ||
		got0.BlockWriteBytes != want.BlockWriteBytes ||
		got0.PIDs != want.PIDs {
		t.Errorf("got sample %+v, want fields matching %+v", got0, want)
	}
	if !got0.RecordedAt.Equal(now) {
		t.Errorf("RecordedAt = %v, want %v", got0.RecordedAt, now)
	}
}

func TestListContainerMetricSamplesEmptyWhenNoneMatch(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	serverID := createTestServer(t, st)

	got, err := st.ListContainerMetricSamples(ctx, serverID, "no-such-container", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListContainerMetricSamples failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d samples, want 0", len(got))
	}
}

func TestPruneContainerMetricSamplesOlderThan(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	serverID := createTestServer(t, st)

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)

	if err := st.InsertContainerMetricSample(ctx, serverID, old, ContainerResourceUsage{ContainerID: "c1"}); err != nil {
		t.Fatalf("failed to insert old sample: %v", err)
	}
	if err := st.InsertContainerMetricSample(ctx, serverID, now, ContainerResourceUsage{ContainerID: "c1"}); err != nil {
		t.Fatalf("failed to insert recent sample: %v", err)
	}

	if err := st.PruneContainerMetricSamplesOlderThan(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("PruneContainerMetricSamplesOlderThan failed: %v", err)
	}

	got, err := st.ListContainerMetricSamples(ctx, serverID, "c1", now.Add(-72*time.Hour))
	if err != nil {
		t.Fatalf("ListContainerMetricSamples failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d samples after prune, want 1 (only the recent one should survive)", len(got))
	}
	if !got[0].RecordedAt.Equal(now) {
		t.Errorf("surviving sample RecordedAt = %v, want %v (the old one should have been pruned)", got[0].RecordedAt, now)
	}
}
