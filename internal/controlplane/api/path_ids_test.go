package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInvalidUUIDParam(t *testing.T) {
	const id = "30c5aaf5-3531-41b9-bf18-c3b1630f63b9"
	cases := []struct {
		pattern, path string
		ok            bool
	}{
		{"GET /api/servers/{id}", "/api/servers/" + id, true},
		{"GET /api/servers/{id}", "/api/servers/not-a-uuid", false},
		{"GET /api/servers/{id}/containers/{containerId}/events", "/api/servers/" + id + "/containers/abc123/events", true},
		{"PUT /api/secrets/{id}/grants/{userId}", "/api/secrets/" + id + "/grants/bob", false},
		{"GET /api/deployments/{id}/revisions/{revision}", "/api/deployments/" + id + "/revisions/3", true},
		{"PUT /api/environment-policies/{environment}", "/api/environment-policies/production", true},
		{"GET /api/servers", "/api/servers", true},
	}
	for _, tc := range cases {
		if _, _, ok := invalidUUIDParam(tc.pattern, tc.path); ok != tc.ok {
			t.Errorf("invalidUUIDParam(%q, %q) ok = %v, want %v", tc.pattern, tc.path, ok, tc.ok)
		}
	}
}

func TestPathIDValidationChecksServerIDQuery(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/schedules", func(w http.ResponseWriter, _ *http.Request) {})
	h := withPathIDValidation(mux)
	cases := map[string]int{
		"/api/schedules": http.StatusOK,
		"/api/schedules?serverId=30c5aaf5-3531-41b9-bf18-c3b1630f63b9": http.StatusOK,
		"/api/schedules?serverId=not-a-uuid":                           http.StatusBadRequest,
	}
	for path, want := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
}
