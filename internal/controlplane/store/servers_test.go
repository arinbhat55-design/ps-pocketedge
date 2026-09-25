package store

import (
	"context"
	"errors"
	"testing"
)

func TestRetireServer(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id := createTestServer(t, st)

	if _, err := st.pool.Exec(ctx, `
		INSERT INTO server_containers (server_id, container_id, name, state)
		VALUES ($1, 'abc', 'web', 'running')
	`, id); err != nil {
		t.Fatalf("seed container: %v", err)
	}

	if err := st.RetireServer(ctx, id); err != nil {
		t.Fatalf("RetireServer: %v", err)
	}

	servers, err := st.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	for _, s := range servers {
		if s.ID == id {
			t.Fatalf("removed server still listed")
		}
	}

	containers, err := st.ListContainers(ctx, id)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(containers) != 0 {
		t.Fatalf("expected container snapshot cleared, got %d", len(containers))
	}

	if _, err := st.GetAgentTokenHash(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed server's agent should not authenticate, got err=%v", err)
	}

	// Still resolvable for deployment history.
	if _, err := st.GetServer(ctx, id); err != nil {
		t.Fatalf("GetServer after removal: %v", err)
	}

	if err := st.RetireServer(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second removal should be ErrNotFound, got %v", err)
	}
}
