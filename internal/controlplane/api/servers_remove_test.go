package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

type fakeConns map[string]bool

func (f fakeConns) IsConnected(id string) bool { return f[id] }

func TestHandleRemoveServer(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping (see deploy/docker-compose.dev.yml)")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)

	id, err := st.CreateServer(ctx, "remove-test", "linux", "amd64", "test", "hash", nil)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), url)
		if err != nil {
			return
		}
		defer conn.Close(context.Background())
		_, _ = conn.Exec(context.Background(), `DELETE FROM servers WHERE id = $1`, id)
	})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	remove := func(conns fakeConns) int {
		req := httptest.NewRequest(http.MethodDelete, "/api/servers/"+id, nil)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		handleRemoveServer(log, st, conns).ServeHTTP(rec, req)
		return rec.Code
	}

	if code := remove(fakeConns{id: true}); code != http.StatusConflict {
		t.Fatalf("connected server: want 409, got %d", code)
	}
	if code := remove(fakeConns{}); code != http.StatusNoContent {
		t.Fatalf("disconnected server: want 204, got %d", code)
	}
	if code := remove(fakeConns{}); code != http.StatusNotFound {
		t.Fatalf("already removed: want 404, got %d", code)
	}
}
