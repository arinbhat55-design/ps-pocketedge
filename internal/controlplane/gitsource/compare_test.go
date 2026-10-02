package gitsource

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// localRepo creates an on-disk repository on branch main with one commit
// per message, a day apart, and returns its path and the commit hashes,
// oldest first.
func localRepo(t *testing.T, messages ...string) (string, []string) {
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
	start := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	var hashes []string
	for i, msg := range messages {
		if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("compose.yaml"); err != nil {
			t.Fatal(err)
		}
		sig := &object.Signature{Name: "Dev", Email: "dev@example.com", When: start.AddDate(0, 0, i)}
		h, err := wt.Commit(msg, &git.CommitOptions{Author: sig, Committer: sig})
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h.String())
	}
	head, err := r.Head()
	if err != nil {
		t.Fatal(err)
	}
	if head.Name().Short() != "main" {
		if err := wt.Checkout(&git.CheckoutOptions{Branch: "refs/heads/main", Create: true}); err != nil {
			t.Fatal(err)
		}
	}
	return dir, hashes
}

func TestCompareCommits(t *testing.T) {
	dir, hashes := localRepo(t, "one", "two", "three", "four")
	repo := Repo{URL: dir}
	ctx := context.Background()
	latest := hashes[3]

	t.Run("behind", func(t *testing.T) {
		cmp, err := CompareCommits(ctx, repo, "main", latest, hashes[1], 20)
		if err != nil {
			t.Fatal(err)
		}
		if cmp.Latest.Hash != latest || cmp.Latest.Message != "four" || !cmp.Latest.Date.Equal(time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)) {
			t.Fatalf("latest = %+v", cmp.Latest)
		}
		if cmp.Deployed == nil || cmp.Deployed.Hash != hashes[1] || cmp.Deployed.Message != "two" {
			t.Fatalf("deployed = %+v", cmp.Deployed)
		}
		if len(cmp.Behind) != 2 || cmp.Behind[0].Hash != hashes[3] || cmp.Behind[1].Hash != hashes[2] || cmp.More {
			t.Fatalf("behind = %+v more=%v", cmp.Behind, cmp.More)
		}
	})

	t.Run("limited", func(t *testing.T) {
		cmp, err := CompareCommits(ctx, repo, "main", latest, hashes[0], 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(cmp.Behind) != 2 || !cmp.More || cmp.Deployed == nil {
			t.Fatalf("behind = %+v more=%v deployed=%+v", cmp.Behind, cmp.More, cmp.Deployed)
		}
	})

	t.Run("up to date", func(t *testing.T) {
		cmp, err := CompareCommits(ctx, repo, "main", latest, latest, 20)
		if err != nil {
			t.Fatal(err)
		}
		if cmp.Deployed == nil || cmp.Deployed.Hash != latest || len(cmp.Behind) != 0 {
			t.Fatalf("cmp = %+v", cmp)
		}
	})

	t.Run("not on branch", func(t *testing.T) {
		cmp, err := CompareCommits(ctx, repo, "main", latest, "0123456789012345678901234567890123456789", 20)
		if err != nil {
			t.Fatal(err)
		}
		if cmp.Deployed != nil || len(cmp.Behind) != 0 || cmp.More || cmp.Latest.Hash != latest {
			t.Fatalf("cmp = %+v", cmp)
		}
	})
}

func TestCompareCommitsMany(t *testing.T) {
	dir, hashes := localRepo(t, "one", "two", "three")
	latest := hashes[2]
	gone := "0123456789012345678901234567890123456789"
	out, err := CompareCommitsMany(context.Background(), Repo{URL: dir}, "main", latest, []string{hashes[0], latest, gone, ""}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if c := out[hashes[0]]; c.Deployed == nil || len(c.Behind) != 2 {
		t.Fatalf("oldest: %+v", c)
	}
	if c := out[latest]; c.Deployed == nil || len(c.Behind) != 0 {
		t.Fatalf("latest: %+v", c)
	}
	if c := out[gone]; c.Deployed != nil || c.Latest.Hash != latest {
		t.Fatalf("gone: %+v", c)
	}
	if c := out[""]; c.Deployed != nil || c.Latest.Hash != latest {
		t.Fatalf("empty: %+v", c)
	}
}
