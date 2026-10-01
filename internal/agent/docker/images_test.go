package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"
)

func TestPushImageChecksRegistryStream(t *testing.T) {
	for _, tc := range []struct {
		name, pushBody string
		wantError      bool
	}{
		{"success", `{"status":"Pushed"}`, false},
		{"registry denied", `{"error":"denied"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tagged, pushed bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/json"):
					_, _ = w.Write([]byte(`{"Id":"sha256:abc","RepoTags":["local:test"]}`))
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/tag"):
					tagged = true
					w.WriteHeader(http.StatusCreated)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/push"):
					pushed = true
					_, _ = w.Write([]byte(tc.pushBody + "\n"))
				default:
					http.Error(w, r.URL.Path, http.StatusNotFound)
				}
			}))
			defer server.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.46"))
			if err != nil {
				t.Fatal(err)
			}
			err = PushImage(context.Background(), cli, "local:test", "registry.example.com/team/app:test", nil)
			if (err != nil) != tc.wantError || !tagged || !pushed {
				t.Fatalf("PushImage error=%v, tagged=%v, pushed=%v", err, tagged, pushed)
			}
		})
	}
}

func TestCleanRepoList(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"empty", []string{}, []string{}},
		{"dangling tag placeholder", []string{"<none>:<none>"}, []string{}},
		{"dangling digest placeholder", []string{"<none>@<none>"}, []string{}},
		{"real tags", []string{"nginx:latest", "nginx:1.27"}, []string{"nginx:latest", "nginx:1.27"}},
		{
			"mixed real and placeholder",
			[]string{"nginx:latest", "<none>:<none>"},
			[]string{"nginx:latest"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanRepoList(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("cleanRepoList(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("cleanRepoList(%v) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

func TestIsDangling(t *testing.T) {
	cases := []struct {
		name     string
		repoTags []string
		want     bool
	}{
		{"no tags at all", nil, true},
		{"only placeholder tag", []string{"<none>:<none>"}, true},
		{"one real tag", []string{"nginx:latest"}, false},
		{"real tag plus placeholder", []string{"nginx:latest", "<none>:<none>"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDangling(tc.repoTags); got != tc.want {
				t.Errorf("isDangling(%v) = %v, want %v", tc.repoTags, got, tc.want)
			}
		})
	}
}

func TestParseImageCreated(t *testing.T) {
	cases := []struct {
		name    string
		created string
		want    int64
	}{
		{"empty string", "", 0},
		{"malformed", "not-a-timestamp", 0},
		{"valid RFC3339Nano", "2025-10-08T12:30:40.123456789Z", 1759926640},
		{"valid RFC3339 without nanos", "2025-10-08T12:30:40Z", 1759926640},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseImageCreated(tc.created); got != tc.want {
				t.Errorf("parseImageCreated(%q) = %d, want %d", tc.created, got, tc.want)
			}
		})
	}
}
