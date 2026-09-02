package docker

import "testing"

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
