package gitsource

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestLiveGitHub exercises the real network path against a public repo.
// Opt-in: GITSOURCE_LIVE=1 go test ./internal/controlplane/gitsource/ -run Live
func TestLiveGitHub(t *testing.T) {
	if os.Getenv("GITSOURCE_LIVE") == "" {
		t.Skip("set GITSOURCE_LIVE=1 to run against github.com")
	}
	repo := Repo{URL: "https://github.com/docker/awesome-compose.git", Provider: ProviderGitHub}
	ctx := context.Background()
	refs, err := ListRefs(ctx, repo)
	if err != nil || len(refs.Branches) == 0 {
		t.Fatalf("ListRefs: %v %+v", err, refs)
	}
	content, commit, err := FetchFile(ctx, repo, "master", "nginx-golang/compose.yaml")
	if err != nil || !strings.Contains(content, "services") || len(commit) != 40 {
		t.Fatalf("FetchFile: %v commit=%q", err, commit)
	}
	commits, err := ListCommits(ctx, repo, "master", "nginx-golang/compose.yaml", 3)
	if err != nil || len(commits) == 0 {
		t.Fatalf("ListCommits: %v", err)
	}
	old, _, err := FetchFileAtCommit(ctx, repo, "master", commits[len(commits)-1].Hash[:10], "nginx-golang/compose.yaml")
	if err != nil || old == "" {
		t.Fatalf("FetchFileAtCommit: %v", err)
	}
	t.Logf("branches=%d tags=%d head=%s commits=%d", len(refs.Branches), len(refs.Tags), commit[:7], len(commits))
}
