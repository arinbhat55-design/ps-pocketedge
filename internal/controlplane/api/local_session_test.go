package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
)

func TestLocalSessionCORSOnlyAllowsDashboardOrigin(t *testing.T) {
	authMgr := auth.NewManager([]byte("test-secret"))
	if err := authMgr.SetDashboardOrigins([]string{"http://localhost:8090"}); err != nil {
		t.Fatal(err)
	}
	handler := withCORS(authMgr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for origin, want := range map[string]string{
		"https://evil.example":  "",
		"http://localhost:3000": "",
		"http://localhost:8090": "http://localhost:8090",
	} {
		r := httptest.NewRequest(http.MethodOptions, "http://localhost:8080/api/auth/local-session", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("origin %s: Access-Control-Allow-Origin = %q, want %q", origin, got, want)
		}
	}
}

func TestLocalSessionRefusalExplainsWhy(t *testing.T) {
	authMgr := auth.NewManager([]byte("test-secret"))
	authMgr.SetLocalSessionsAllowed(true)
	handler := handleLocalSession(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, authMgr, true)
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/auth/local-session", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if body := w.Body.String(); body != "local access unavailable: request was relayed by a proxy\n" {
		t.Fatalf("body = %q", body)
	}
}
