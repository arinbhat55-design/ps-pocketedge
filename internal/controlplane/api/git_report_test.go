package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/gitsource"
)

// gitRepoWithCommits creates an on-disk repository whose branch main has
// one commit per message, oldest first, and returns its path and hashes.
func gitRepoWithCommits(t *testing.T, messages ...string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for i, msg := range messages {
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("compose.yaml"); err != nil {
			t.Fatal(err)
		}
		sig := &object.Signature{Name: "Dev", When: time.Date(2026, 9, 1+i, 10, 0, 0, 0, time.UTC)}
		h, err := wt.Commit(msg, &git.CommitOptions{Author: sig, Committer: sig})
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h.String())
	}
	if err := wt.Checkout(&git.CheckoutOptions{Branch: "refs/heads/main", Create: true}); err != nil {
		t.Fatal(err)
	}
	return dir, hashes
}

func TestCheckGitBranch(t *testing.T) {
	dir, hashes := gitRepoWithCommits(t, "one", "two", "three")
	behind := &gitReportRow{DeployedCommit: hashes[0]}
	pending := &gitReportRow{DeployedCommit: hashes[1], PendingRequests: 1}
	current := &gitReportRow{DeployedCommit: hashes[2]}
	never := &gitReportRow{}
	checkGitBranch(context.Background(), gitsource.Repo{URL: dir}, "main", []*gitReportRow{behind, pending, current, never})

	if behind.Status != gitStatusBehind || len(behind.CommitsBehind) != 2 || behind.DeployedCommitInfo.Message != "one" ||
		behind.LatestCommitInfo.Message != "three" || behind.LatestCommit != hashes[2] {
		t.Fatalf("behind: %+v", behind)
	}
	if pending.Status != gitStatusPending || len(pending.CommitsBehind) != 1 {
		t.Fatalf("pending: %+v", pending)
	}
	if current.Status != gitStatusUpToDate || len(current.CommitsBehind) != 0 {
		t.Fatalf("current: %+v", current)
	}
	if never.Status != gitStatusNotDeployed {
		t.Fatalf("never: %+v", never)
	}

	unreachable := &gitReportRow{DeployedCommit: hashes[0]}
	checkGitBranch(context.Background(), gitsource.Repo{URL: filepath.Join(dir, "missing")}, "main", []*gitReportRow{unreachable})
	if unreachable.Status != gitStatusError || unreachable.Error == "" {
		t.Fatalf("unreachable: %+v", unreachable)
	}
}
