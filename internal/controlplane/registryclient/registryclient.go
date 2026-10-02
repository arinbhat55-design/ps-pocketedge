// Package registryclient talks to container registries directly (Docker
// Hub and any private registry configured via internal/controlplane/store's
// Registry rows) for the operations that don't require an agent: resolving
// a tag's current digest ("is a newer image available"), listing a repo's
// tags ("select image version/tag"), and searching. It's built on
// go-containerregistry rather than a hand-rolled Docker Registry HTTP API
// v2 client (including Docker Hub's separate OAuth token dance) since that
// library already handles both correctly.
package registryclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Credentials authenticates against a specific registry; a zero value
// means anonymous/public access.
type Credentials struct {
	Username string
	Password string
}

func (c Credentials) option() remote.Option {
	if c.Username == "" && c.Password == "" {
		return remote.WithAuth(authn.Anonymous)
	}
	return remote.WithAuth(&authn.Basic{Username: c.Username, Password: c.Password})
}

// ResolveDigest returns the current remote digest for ref's tag (e.g.
// "nginx:1.27" or "myregistry.com/team/app:latest"), for comparing against
// a locally-pulled image's RepoDigests to detect "is a newer image
// available".
func ResolveDigest(ctx context.Context, ref string, creds Credentials) (string, error) {
	tag, err := name.ParseReference(ref)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %q: %w", ref, err)
	}
	desc, err := remote.Get(tag, creds.option(), remote.WithContext(ctx))
	if err != nil {
		return "", err
	}
	return desc.Digest.String(), nil
}

// Platforms resolves ref (same forms as ResolveDigest) and returns every
// CPU architecture it supports — the architectures listed in a multi-arch
// manifest list, or the single architecture of a plain image manifest.
// Used for "Check image availability" (a returned error means the image
// doesn't exist or isn't reachable with creds) and "Check host
// architecture compatibility" together, since both need this same lookup.
func Platforms(ctx context.Context, ref string, creds Credentials) ([]string, error) {
	tag, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference %q: %w", ref, err)
	}
	desc, err := remote.Get(tag, creds.option(), remote.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	if desc.MediaType.IsIndex() {
		idx, err := desc.ImageIndex()
		if err != nil {
			return nil, err
		}
		manifest, err := idx.IndexManifest()
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		platforms := make([]string, 0, len(manifest.Manifests))
		for _, m := range manifest.Manifests {
			if m.Platform == nil {
				continue
			}
			arch := m.Platform.Architecture
			// "unknown" is buildx's convention for attestation/SBOM
			// manifest entries riding alongside the real per-platform
			// ones in the same index (every real entry above is paired
			// with one) — not a deployable platform, so it'd otherwise
			// pollute the list and never match a real server arch.
			if arch == "" || arch == "unknown" || seen[arch] {
				continue
			}
			seen[arch] = true
			platforms = append(platforms, arch)
		}
		return platforms, nil
	}

	img, err := desc.Image()
	if err != nil {
		return nil, err
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	if cfg.Architecture == "" {
		return nil, nil
	}
	return []string{cfg.Architecture}, nil
}

// ListTags returns every tag published for repo (e.g. "nginx" or
// "myregistry.com/team/app"), most relevant for the "select image version
// or tag" picker.
func ListTags(ctx context.Context, repo string, creds Credentials) ([]string, error) {
	r, err := name.NewRepository(repo)
	if err != nil {
		return nil, fmt.Errorf("invalid repository %q: %w", repo, err)
	}
	tags, err := remote.List(r, creds.option(), remote.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	sort.Strings(tags)
	return tags, nil
}

// SearchResult is one hit from either Docker Hub's public search or a
// private registry's catalog listing.
type SearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	StarCount   int    `json:"starCount,omitempty"`
	Official    bool   `json:"official,omitempty"`
}

// dockerHubSearchTimeout bounds the public search API call — a public,
// unauthenticated third-party HTTP request should never hang a request
// indefinitely.
const dockerHubSearchTimeout = 10 * time.Second

// SearchDockerHub queries Docker Hub's public (unauthenticated) repository
// search API.
func SearchDockerHub(ctx context.Context, query string) ([]SearchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, dockerHubSearchTimeout)
	defer cancel()

	u := "https://hub.docker.com/v2/search/repositories/?query=" + url.QueryEscape(query) + "&page_size=25"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("docker hub search failed: %s: %s", resp.Status, string(body))
	}

	var parsed struct {
		Results []struct {
			RepoName   string `json:"repo_name"`
			ShortDesc  string `json:"short_description"`
			StarCount  int    `json:"star_count"`
			IsOfficial bool   `json:"is_official"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	results := make([]SearchResult, len(parsed.Results))
	for i, r := range parsed.Results {
		results[i] = SearchResult{
			Name:        r.RepoName,
			Description: r.ShortDesc,
			StarCount:   r.StarCount,
			Official:    r.IsOfficial,
		}
	}
	return results, nil
}

// SearchCatalog does a best-effort search of a private registry's
// repository catalog (Docker Registry HTTP API v2's `/v2/_catalog`),
// filtering client-side by substring since v2 has no server-side search.
// Many registries disable or paginate _catalog; this returns whatever a
// single unpaginated call yields, which is an accepted MVP limitation.
func SearchCatalog(ctx context.Context, registryURL, query string, creds Credentials) ([]SearchResult, error) {
	reg, err := name.NewRegistry(stripScheme(registryURL))
	if err != nil {
		return nil, fmt.Errorf("invalid registry url %q: %w", registryURL, err)
	}

	repos, err := remote.Catalog(ctx, reg, creds.option())
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)
	results := make([]SearchResult, 0, len(repos))
	for _, repo := range repos {
		if query == "" || strings.Contains(strings.ToLower(repo), query) {
			results = append(results, SearchResult{Name: repo})
		}
	}
	return results, nil
}

func stripScheme(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return strings.TrimSuffix(u, "/")
}
