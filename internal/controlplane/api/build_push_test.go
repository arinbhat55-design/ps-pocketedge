package api

import "testing"

func TestRegistryPushRef(t *testing.T) {
	for _, tc := range []struct {
		url, repository, want string
	}{
		{"registry.example.com:5000", "team/api", "registry.example.com:5000/team/api:abc123-settings"},
		{"https://registry.example.com/base/", "team/api", "registry.example.com/base/team/api:abc123-settings"},
	} {
		got, err := registryPushRef(tc.url, tc.repository, "pspe-build/project-api:abc123-settings")
		if err != nil || got != tc.want {
			t.Fatalf("registryPushRef(%q, %q) = %q, %v; want %q", tc.url, tc.repository, got, err, tc.want)
		}
	}
}

func TestRegistryPushRefRejectsCredentialRedirects(t *testing.T) {
	for _, tc := range []struct{ url, repository string }{
		{"https://user:secret@registry.example.com", "team/api"},
		{"https://registry.example.com/?next=evil", "team/api"},
		{"https://registry.example.com", "evil.com/team/api:latest"},
		{"https://registry.example.com", "../api"},
	} {
		if ref, err := registryPushRef(tc.url, tc.repository, "pspe-build/project-api:abc123-settings"); err == nil {
			t.Fatalf("accepted %q and %q as %q", tc.url, tc.repository, ref)
		}
	}
}
