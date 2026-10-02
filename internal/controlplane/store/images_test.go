package store

import (
	"strings"
	"testing"
)

func TestMatchesImagePattern(t *testing.T) {
	cases := []struct {
		name    string
		ref     string
		pattern string
		want    bool
	}{
		{"exact match", "nginx", "nginx", true},
		{"exact match with tag", "nginx:latest", "nginx:latest", true},
		{"bare repo matches any tag", "nginx:alpine", "nginx", true},
		{"bare repo matches a digest", "nginx@sha256:" + strings.Repeat("a", 64), "nginx", true},
		{"Docker Hub names are normalized", "docker.io/library/nginx:alpine", "nginx", true},
		{"normalized pattern matches short ref", "nginx:alpine", "docker.io/library/nginx", true},
		{"bare repo does not match a repo sharing its prefix", "nginx-evil/miner:latest", "nginx", false},
		{"bare repo does not match a longer name", "nginxattacker:latest", "nginx", false},
		{"tag must match", "nginx:1.27", "nginx:alpine", false},
		{"untagged ref is latest for tag globs", "nginx", "nginx:latest*", true},
		{"tag glob", "nginx:alpine", "nginx:*", true},
		{"tag glob with prefix", "nginx:1.27-alpine", "nginx:1.*", true},
		{"tag glob rejects other tags", "nginx:alpine", "nginx:1.*", false},
		{"tag glob does not cross repositories", "nginx-evil:1.0", "nginx:*", false},
		{"registry-scoped glob", "ghcr.io/acme/app:2", "ghcr.io/acme/*", true},
		{"registry-scoped glob rejects other orgs", "ghcr.io/other/app:2", "ghcr.io/acme/*", false},
		{"legacy trailing-slash prefix", "myregistry.com/team/app:latest", "myregistry.com/team/", true},
		{"unrelated repo", "redis:7", "nginx", false},
		{"prefix must anchor at the start", "official-nginx", "nginx", false},
		{"empty pattern matches nothing", "redis:7", "", false},
		{"star matches everything", "redis:7", "*", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesImagePattern(tc.ref, tc.pattern); got != tc.want {
				t.Errorf("matchesImagePattern(%q, %q) = %v, want %v", tc.ref, tc.pattern, got, tc.want)
			}
		})
	}
}

func TestValidateImagePattern(t *testing.T) {
	for _, ok := range []string{"nginx", "nginx:1.27", "nginx:*", "ghcr.io/acme/*", "myregistry.com/team/", "*"} {
		if err := ValidateImagePattern(ok); err != nil {
			t.Errorf("ValidateImagePattern(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "NGINX", "nginx::1", "has space"} {
		if err := ValidateImagePattern(bad); err == nil {
			t.Errorf("ValidateImagePattern(%q) = nil, want an error", bad)
		}
	}
}
