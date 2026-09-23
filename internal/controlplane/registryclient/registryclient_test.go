package registryclient

import (
	"context"
	"testing"
)

func TestStripScheme(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"https with trailing slash", "https://registry.example.com/", "registry.example.com"},
		{"http, no trailing slash", "http://registry.example.com", "registry.example.com"},
		{"already bare host", "registry.example.com", "registry.example.com"},
		{"host with port", "https://registry.example.com:5000", "registry.example.com:5000"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripScheme(tc.in); got != tc.want {
				t.Errorf("stripScheme(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPlatformsRejectsInvalidReferenceWithoutNetworkAccess(t *testing.T) {
	// An invalid ref must fail at name.ParseReference, before any network
	// call — this doesn't require network access to test.
	if _, err := Platforms(context.Background(), "not a valid ref!!", Credentials{}); err == nil {
		t.Error("expected an error for an invalid image reference")
	}
}

func TestCredentialsOptionAnonymousVsBasic(t *testing.T) {
	// option() must not panic and must return a non-nil remote.Option in
	// both the anonymous and authenticated cases — the two branches this
	// function actually decides between.
	if opt := (Credentials{}).option(); opt == nil {
		t.Error("Credentials{}.option() returned nil for the anonymous case")
	}
	if opt := (Credentials{Username: "u", Password: "p"}).option(); opt == nil {
		t.Error("Credentials{...}.option() returned nil for the authenticated case")
	}
}
