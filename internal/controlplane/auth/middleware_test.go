package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestViewerAccessPolicy(t *testing.T) {
	m := NewManager([]byte("test-secret"))
	token, err := m.IssueToken("u1", "viewer@example.com", "viewer", 1)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		pattern string
		want    int
	}{
		{"GET /api/containers", http.StatusOK},
		{"POST /api/servers/{id}/containers/{containerId}/inspect", http.StatusOK},
		{"POST /api/servers/{id}/containers/{containerId}/action", http.StatusForbidden},
		{"POST /api/servers/{id}/containers", http.StatusForbidden},
		{"POST /api/deployments", http.StatusForbidden},
		{"POST /api/secrets/{id}/reveal", http.StatusOK},
		{"POST /api/secrets/{id}/download", http.StatusOK},
		{"POST /api/secrets/{id}/revoke", http.StatusForbidden},
		{"DELETE /api/servers/{id}/volumes/{name}", http.StatusForbidden},
		{"GET /api/servers/{id}/volumes/{name}/files/content", http.StatusForbidden},
		{"GET /api/compose-files", http.StatusForbidden},
		{"GET /api/env-var-groups/{id}", http.StatusForbidden},
		{"GET /api/deployments/{id}/revisions/{revision}", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.Handle(tt.pattern, m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})))
			method, path, _ := strings.Cut(tt.pattern, " ")
			r := httptest.NewRequest(method, path, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestRoleChangesApplyToExistingTokens(t *testing.T) {
	m := NewManager([]byte("test-secret"))
	role := "admin"
	m.SetUserLookup(func(context.Context, string) (UserState, error) { return UserState{Role: role}, nil })
	token, err := m.IssueToken("u1", "user@example.com", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	role = "viewer"
	claims, err := m.AuthenticateRequest(httptest.NewRequest(http.MethodGet, "/api/containers", nil), token)
	if err != nil || claims.Role != "viewer" {
		t.Fatalf("demotion not applied: claims=%+v err=%v", claims, err)
	}
}

func TestRoleLookupFailureIsNotASignOut(t *testing.T) {
	m := NewManager([]byte("test-secret"))
	m.SetUserLookup(func(context.Context, string) (UserState, error) { return UserState{}, errors.New("db down") })
	token, err := m.IssueToken("u1", "user@example.com", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/containers", m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})))
	r := httptest.NewRequest(http.MethodGet, "/api/containers", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestDeletedUserTokenIsRejected(t *testing.T) {
	m := NewManager([]byte("test-secret"))
	m.SetUserLookup(func(context.Context, string) (UserState, error) { return UserState{}, nil })
	token, err := m.IssueToken("u1", "user@example.com", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AuthenticateRequest(httptest.NewRequest(http.MethodGet, "/", nil), token)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestPasswordChangeEndsEarlierSessions(t *testing.T) {
	m := NewManager([]byte("test-secret"))
	state := UserState{Role: "admin", SessionVersion: 1}
	m.SetUserLookup(func(context.Context, string) (UserState, error) { return state, nil })
	old, err := m.IssueToken("u1", "user@example.com", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}

	// The password changed — even within the same second the token was
	// issued in.
	state.SessionVersion = 2
	if _, err := m.AuthenticateRequest(httptest.NewRequest(http.MethodGet, "/", nil), old); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("old token: err = %v, want ErrInvalidToken", err)
	}

	fresh, err := m.IssueToken("u1", "user@example.com", "admin", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AuthenticateRequest(httptest.NewRequest(http.MethodGet, "/", nil), fresh); err != nil {
		t.Fatalf("token issued after the change: err = %v", err)
	}
}
