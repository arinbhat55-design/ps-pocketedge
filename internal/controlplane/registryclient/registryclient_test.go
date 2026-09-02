package registryclient

import "testing"

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
