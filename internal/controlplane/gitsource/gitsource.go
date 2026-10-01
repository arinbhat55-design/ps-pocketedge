// Package gitsource reads Compose files out of Git repositories for
// Git-based deployment: listing a remote's branches/tags, fetching a file at
// a branch, tag, or commit, and listing the commits that touched it. It
// talks to any Git host (GitHub, GitLab, Azure DevOps, Bitbucket, or a
// self-hosted server) over HTTPS with go-git, cloning into memory — nothing
// is written to the control plane's disk, and no git binary is needed.
package gitsource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// Providers the UI offers. They only change the default HTTP username used
// with a token (each host expects a different one) and how webhooks are
// parsed — cloning itself is plain Git for all of them.
const (
	ProviderGitHub      = "github"
	ProviderGitLab      = "gitlab"
	ProviderAzureDevOps = "azure_devops"
	ProviderBitbucket   = "bitbucket"
	ProviderGeneric     = "generic"
)

// ValidProvider reports whether p is one of the supported providers.
func ValidProvider(p string) bool {
	switch p {
	case ProviderGitHub, ProviderGitLab, ProviderAzureDevOps, ProviderBitbucket, ProviderGeneric:
		return true
	}
	return false
}

// operationTimeout bounds any single remote operation.
const operationTimeout = 60 * time.Second

// maxComposeFileBytes guards against importing something that clearly isn't
// a Compose file.
const maxComposeFileBytes = 1 << 20

// Repo is what gitsource needs to reach a repository.
type Repo struct {
	URL      string
	Provider string
	Username string
	Token    string
}

func (r Repo) auth() transport.AuthMethod {
	if r.Token == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: r.AuthUsername(), Password: r.Token}
}

// AuthUsername is the HTTP username sent with Token: the configured one,
// or the one the provider expects with an access token.
func (r Repo) AuthUsername() string {
	if r.Username != "" {
		return r.Username
	}
	switch r.Provider {
	case ProviderGitLab:
		return "oauth2"
	case ProviderBitbucket:
		return "x-token-auth"
	default:
		return "git"
	}
}

// Ref is one branch or tag on the remote.
type Ref struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

// Refs lists a remote's branches and tags.
type Refs struct {
	Branches []Ref `json:"branches"`
	Tags     []Ref `json:"tags"`
}

// ListRefs lists the remote's branches and tags (`git ls-remote`). It also
// serves as the connectivity/credentials check when a repository is added.
func ListRefs(ctx context.Context, repo Repo) (*Refs, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{repo.URL}})
	list, err := remote.ListContext(ctx, &git.ListOptions{Auth: repo.auth()})
	if err != nil {
		return nil, fmt.Errorf("failed to list remote refs: %w", err)
	}
	out := &Refs{Branches: []Ref{}, Tags: []Ref{}}
	peeled := map[string]string{}
	for _, r := range list {
		name := r.Name().String()
		if strings.HasSuffix(name, "^{}") {
			peeled[strings.TrimSuffix(name, "^{}")] = r.Hash().String()
		}
	}
	for _, r := range list {
		name := r.Name()
		switch {
		case name.IsBranch():
			out.Branches = append(out.Branches, Ref{Name: name.Short(), Commit: r.Hash().String()})
		case name.IsTag() && !strings.HasSuffix(name.String(), "^{}"):
			commit := r.Hash().String()
			if p, ok := peeled[name.String()]; ok {
				commit = p
			}
			out.Tags = append(out.Tags, Ref{Name: name.Short(), Commit: commit})
		}
	}
	sort.Slice(out.Branches, func(i, j int) bool { return out.Branches[i].Name < out.Branches[j].Name })
	sort.Slice(out.Tags, func(i, j int) bool { return out.Tags[i].Name > out.Tags[j].Name })
	return out, nil
}

// ResolveRef returns the commit a branch or tag currently points to on the
// remote, without cloning — cheap enough for drift checks.
func ResolveRef(ctx context.Context, repo Repo, ref string) (string, error) {
	refs, err := ListRefs(ctx, repo)
	if err != nil {
		return "", err
	}
	for _, b := range refs.Branches {
		if b.Name == ref {
			return b.Commit, nil
		}
	}
	for _, t := range refs.Tags {
		if t.Name == ref {
			return t.Commit, nil
		}
	}
	if looksLikeCommit(ref) {
		return ref, nil
	}
	return "", fmt.Errorf("ref %q not found on the remote", ref)
}

func looksLikeCommit(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// referenceName resolves ref to a full reference, preferring a branch over
// a tag of the same name.
func referenceName(ctx context.Context, repo Repo, ref string) (plumbing.ReferenceName, error) {
	refs, err := ListRefs(ctx, repo)
	if err != nil {
		return "", err
	}
	for _, b := range refs.Branches {
		if b.Name == ref {
			return plumbing.NewBranchReferenceName(ref), nil
		}
	}
	for _, t := range refs.Tags {
		if t.Name == ref {
			return plumbing.NewTagReferenceName(ref), nil
		}
	}
	return "", fmt.Errorf("branch or tag %q not found on the remote", ref)
}

// clone clones one branch/tag into memory, with only the given depth of
// history (0 = full history of that ref).
func clone(ctx context.Context, repo Repo, ref string, depth int) (*git.Repository, error) {
	refName, err := referenceName(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	r, err := git.CloneContext(ctx, memory.NewStorage(), nil, &git.CloneOptions{
		URL:           repo.URL,
		Auth:          repo.auth(),
		ReferenceName: refName,
		SingleBranch:  true,
		Depth:         depth,
		Tags:          git.NoTags,
		NoCheckout:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to clone %s: %w", ref, err)
	}
	return r, nil
}

func readFile(commit *object.Commit, path string) (string, error) {
	file, err := commit.File(strings.TrimPrefix(path, "/"))
	if errors.Is(err, object.ErrFileNotFound) {
		return "", fmt.Errorf("file %q not found at commit %s", path, commit.Hash.String()[:7])
	}
	if err != nil {
		return "", err
	}
	if file.Size > maxComposeFileBytes {
		return "", fmt.Errorf("file %q is %d bytes — too large for a Compose file", path, file.Size)
	}
	reader, err := file.Reader()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// FetchFile returns path's content at the tip of ref (a branch or tag) and
// the commit it came from.
func FetchFile(ctx context.Context, repo Repo, ref, path string) (content, commit string, err error) {
	r, err := clone(ctx, repo, ref, 1)
	if err != nil {
		return "", "", err
	}
	head, err := r.Head()
	if err != nil {
		return "", "", err
	}
	c, err := r.CommitObject(head.Hash())
	if err != nil {
		return "", "", err
	}
	content, err = readFile(c, path)
	if err != nil {
		return "", "", err
	}
	return content, c.Hash.String(), nil
}

// FetchFileAtCommit returns path's content at a specific commit reachable
// from ref — "Roll back to a previous commit". It needs ref's history, so
// it clones without a depth limit.
func FetchFileAtCommit(ctx context.Context, repo Repo, ref, commit, path string) (string, string, error) {
	r, err := clone(ctx, repo, ref, 0)
	if err != nil {
		return "", "", err
	}
	hash, err := resolveCommit(r, commit)
	if err != nil {
		return "", "", err
	}
	c, err := r.CommitObject(hash)
	if err != nil {
		return "", "", fmt.Errorf("commit %s not found on %s: %w", commit, ref, err)
	}
	content, err := readFile(c, path)
	if err != nil {
		return "", "", err
	}
	return content, c.Hash.String(), nil
}

// resolveCommit expands an abbreviated commit hash.
func resolveCommit(r *git.Repository, commit string) (plumbing.Hash, error) {
	if len(commit) == 40 {
		return plumbing.NewHash(commit), nil
	}
	h, err := r.ResolveRevision(plumbing.Revision(commit))
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("commit %s not found: %w", commit, err)
	}
	return *h, nil
}

// Commit is one entry of a file's history.
type Commit struct {
	Hash    string    `json:"hash"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
}

// ListCommits returns up to limit most recent commits on ref that changed
// path, newest first. Only commits where the file exists are included —
// they're offered as rollback targets, and go-git's path filter also
// reports commits around a rename/deletion where the file is absent.
func ListCommits(ctx context.Context, repo Repo, ref, path string, limit int) ([]Commit, error) {
	if limit <= 0 {
		limit = 30
	}
	r, err := clone(ctx, repo, ref, 0)
	if err != nil {
		return nil, err
	}
	head, err := r.Head()
	if err != nil {
		return nil, err
	}
	target := strings.TrimPrefix(path, "/")
	options := &git.LogOptions{From: head.Hash()}
	if target != "" {
		options.PathFilter = func(p string) bool { return p == target }
	}
	iter, err := r.Log(options)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	out := []Commit{}
	for len(out) < limit {
		c, err := iter.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if target != "" {
			if _, err := c.File(target); err != nil {
				continue
			}
		}
		out = append(out, toCommit(c))
	}
	return out, nil
}

// toCommit summarizes c: its hash, the first line of its message, and its
// author and authored date.
func toCommit(c *object.Commit) Commit {
	msg := strings.TrimSpace(c.Message)
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return Commit{Hash: c.Hash.String(), Message: msg, Author: c.Author.Name, Date: c.Author.When}
}

// Comparison is how a deployed commit relates to the tip of a branch/tag.
type Comparison struct {
	Latest Commit
	// Deployed is nil when the deployed commit isn't in the ref's history
	// (the branch was force-pushed or rebased since), or is too far back
	// to look for.
	Deployed *Commit
	// Behind is the commits on the ref after Deployed, newest first, up to
	// the limit; More is set when there are more than that. Both are empty
	// when Deployed is nil, since the count is unknown then.
	Behind []Commit
	More   bool
}

// maxCompareWalk bounds how far back CompareCommits looks for the
// deployed commits.
const maxCompareWalk = 2000

// CompareCommits describes the commits between deployed and ref's tip,
// latest being what ResolveRef returned for ref.
func CompareCommits(ctx context.Context, repo Repo, ref, latest, deployed string, limit int) (*Comparison, error) {
	out, err := CompareCommitsMany(ctx, repo, ref, latest, []string{deployed}, limit)
	if err != nil {
		return nil, err
	}
	return out[deployed], nil
}

// CompareCommitsMany is CompareCommits for several deployed commits on the
// same ref (e.g. one per deployment tracking it), with a single clone.
// When every deployed commit is latest (or empty) only the tip is
// fetched; otherwise ref's history is cloned.
func CompareCommitsMany(ctx context.Context, repo Repo, ref, latest string, deployed []string, limit int) (map[string]*Comparison, error) {
	if limit <= 0 {
		limit = 20
	}
	wanted := map[string]bool{}
	for _, c := range deployed {
		if c != "" && c != latest {
			wanted[c] = true
		}
	}
	depth := 0
	if len(wanted) == 0 {
		depth = 1
	}
	r, err := clone(ctx, repo, ref, depth)
	if err != nil {
		return nil, err
	}
	head, err := r.Head()
	if err != nil {
		return nil, err
	}
	iter, err := r.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	// history is ref's commits newest first, as far back as needed to
	// find every deployed commit; found maps each one to its position.
	var history []Commit
	found := map[string]int{}
	remaining := len(wanted) + 1 // the deployed commits, plus the tip
	for n := 0; n < maxCompareWalk && remaining > 0; n++ {
		c, err := iter.Next()
		// A shallow clone ends at the tip — reached only when ref moved
		// after latest was resolved.
		if errors.Is(err, io.EOF) || (err != nil && depth == 1 && n > 0) {
			break
		}
		if err != nil {
			return nil, err
		}
		commit := toCommit(c)
		history = append(history, commit)
		if _, seen := found[commit.Hash]; !seen {
			found[commit.Hash] = n
			if n == 0 || wanted[commit.Hash] {
				remaining--
			}
		}
	}
	if len(history) == 0 {
		return nil, fmt.Errorf("%s has no commits", ref)
	}
	out := map[string]*Comparison{}
	for _, d := range deployed {
		cmp := &Comparison{Latest: history[0]}
		if i, ok := found[d]; ok && d != "" {
			cmp.Deployed = &history[i]
			cmp.Behind = history[:min(i, limit)]
			cmp.More = i > limit
		}
		out[d] = cmp
	}
	return out, nil
}
