package api

import "testing"

func TestContainsDigest(t *testing.T) {
	cases := []struct {
		name         string
		repoDigest   string
		remoteDigest string
		want         bool
	}{
		{
			"matching digest",
			"alpine@sha256:6baf43584bcb78f2e5847d1de515f23499913ac9f12bdf834811a3145eb11ca1",
			"sha256:6baf43584bcb78f2e5847d1de515f23499913ac9f12bdf834811a3145eb11ca1",
			true,
		},
		{
			"different digest",
			"alpine@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			false,
		},
		{"empty repo digest", "", "sha256:abc", false},
		{"remote digest longer than repo digest", "sha256:ab", "sha256:abcdef", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsDigest(tc.repoDigest, tc.remoteDigest); got != tc.want {
				t.Errorf("containsDigest(%q, %q) = %v, want %v", tc.repoDigest, tc.remoteDigest, got, tc.want)
			}
		})
	}
}
