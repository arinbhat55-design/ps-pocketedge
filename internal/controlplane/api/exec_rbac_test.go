package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
)

func TestViewerCannotOpenContainerExec(t *testing.T) {
	m := auth.NewManager([]byte("test-secret"))
	token, err := m.IssueToken("u1", "viewer@example.com", "viewer", 1)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/servers/s1/containers/c1/exec?token="+token, nil)
	w := httptest.NewRecorder()
	handleContainerExec(nil, m, nil, nil)(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}
