package build

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// source is where a build's code comes from.
type source struct {
	URL      string
	Ref      string
	Commit   string
	Username string
	Token    string
}

func (s source) auth() transport.AuthMethod {
	if s.Token == "" {
		return nil
	}
	user := s.Username
	if user == "" {
		user = "git"
	}
	return &githttp.BasicAuth{Username: user, Password: s.Token}
}

// checkout clones src into dir and checks out src.Commit, returning the
// commit. It first clones only the tip of src.Ref, which is all
// that's needed when the commit is still the branch's latest (the usual
// case: a build started by a push); if the branch has moved on since, it
// clones the ref's full history instead so the older commit is reachable.
func checkout(ctx context.Context, dir string, src source) (*object.Commit, error) {
	repo, err := cloneRef(ctx, dir, src, 1)
	if err != nil {
		return nil, err
	}
	hash, err := wantedCommit(repo, src.Commit)
	if err != nil {
		return nil, err
	}
	commit, err := repo.CommitObject(hash)
	if err != nil {
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
		if repo, err = cloneRef(ctx, dir, src, 0); err != nil {
			return nil, err
		}
		if commit, err = repo.CommitObject(hash); err != nil {
			return nil, fmt.Errorf("commit %s not found on %s", shortHash(src.Commit), src.Ref)
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, err
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: commit.Hash, Force: true}); err != nil {
		return nil, fmt.Errorf("failed to check out %s: %w", shortHash(commit.Hash.String()), err)
	}
	return commit, nil
}

// contextTree returns the hash of the Git tree at contextPath in commit:
// it changes exactly when a file in the build context changes.
func contextTree(commit *object.Commit, contextPath string) (string, error) {
	tree, err := commit.Tree()
	if err != nil {
		return "", err
	}
	if contextPath != "." {
		if tree, err = tree.Tree(contextPath); err != nil {
			return "", err
		}
	}
	return tree.Hash.String(), nil
}

// cloneRef clones src.Ref into dir without checking out. The agent isn't
// told whether the ref is a branch or a tag, so it tries a branch first,
// the same preference the control plane uses.
func cloneRef(ctx context.Context, dir string, src source, depth int) (*git.Repository, error) {
	candidates := []plumbing.ReferenceName{""}
	if src.Ref != "" {
		candidates = []plumbing.ReferenceName{plumbing.NewBranchReferenceName(src.Ref), plumbing.NewTagReferenceName(src.Ref)}
	}
	var lastErr error
	for _, name := range candidates {
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
		repo, err := git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{
			URL:           src.URL,
			Auth:          src.auth(),
			ReferenceName: name,
			SingleBranch:  name != "",
			Depth:         depth,
			Tags:          git.NoTags,
			NoCheckout:    true,
		})
		if err == nil {
			return repo, nil
		}
		lastErr = err
		if !isRefNotFound(err) {
			break
		}
	}
	if isRefNotFound(lastErr) {
		return nil, fmt.Errorf("branch or tag %q not found in the repository", src.Ref)
	}
	return nil, fmt.Errorf("failed to clone the repository: %w", lastErr)
}

func wantedCommit(repo *git.Repository, commit string) (plumbing.Hash, error) {
	if commit != "" {
		if !plumbing.IsHash(commit) {
			return plumbing.ZeroHash, fmt.Errorf("invalid commit %q", commit)
		}
		return plumbing.NewHash(commit), nil
	}
	head, err := repo.Head()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return head.Hash(), nil
}

func isRefNotFound(err error) bool {
	var noMatch git.NoMatchingRefSpecError
	return errors.As(err, &noMatch) || errors.Is(err, plumbing.ErrReferenceNotFound)
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}
