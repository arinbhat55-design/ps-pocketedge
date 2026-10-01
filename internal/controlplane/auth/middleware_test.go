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
	token, err := m.IssueToken("u1", "viewer@example.com", "viewer")
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
	m.SetRoleLookup(func(context.Context, string) (string, error) { return role, nil })
	token, err := m.IssueToken("u1", "user@example.com", "admin")
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
	m.SetRoleLookup(func(context.Context, string) (string, error) { return "", errors.New("db down") })
	token, err := m.IssueToken("u1", "user@example.com", "admin")
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
	m.SetRoleLookup(func(context.Context, string) (string, error) { return "", nil })
	token, err := m.IssueToken("u1", "user@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AuthenticateRequest(httptest.NewRequest(http.MethodGet, "/", nil), token)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}
