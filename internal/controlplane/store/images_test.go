package store

import "testing"

func TestMatchesImagePattern(t *testing.T) {
	cases := []struct {
		name    string
		ref     string
		pattern string
		want    bool
	}{
		{"exact match", "nginx", "nginx", true},
		{"exact match with tag", "nginx:latest", "nginx:latest", true},
		{"bare repo does not match a tagged ref by exact equality alone", "nginx:latest", "nginx", true}, // prefix match
		{"prefix match on registry scope", "myregistry.com/team/app:latest", "myregistry.com/team/", true},
		{"unrelated repo", "redis:7", "nginx", false},
		{"prefix must anchor at the start", "official-nginx", "nginx", false},
		{"empty pattern matches everything (prefix of empty string)", "redis:7", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesImagePattern(tc.ref, tc.pattern); got != tc.want {
				t.Errorf("matchesImagePattern(%q, %q) = %v, want %v", tc.ref, tc.pattern, got, tc.want)
			}
		})
	}
}
