package api

import "testing"

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
