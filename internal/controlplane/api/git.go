package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/compose"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/gitsource"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// maxWebhookBody bounds an inbound webhook payload.
const maxWebhookBody = 5 << 20

func toGitRepo(g *store.GitRepository) gitsource.Repo {
	return gitsource.Repo{URL: g.URL, Provider: g.Provider, Username: g.Username, Token: g.Token}
}

// recordAudit is the audit helper for handlers that don't hold a deployer.
func recordAudit(r *http.Request, log *slog.Logger, st *store.Store, action, entityType, entityID, summary string, details any) {
	a := actorFromRequest(r)
	if err := st.RecordAudit(r.Context(), a.ID, a.Local, action, entityType, entityID, summary, details); err != nil {
		log.Error("failed to record audit event", "action", action, "error", err)
	}
}

// gitRepoWebhookInfo is how to configure a repository's push webhook.
type gitRepoWebhookInfo struct {
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

func webhookInfo(publicURL string, g *store.GitRepository) gitRepoWebhookInfo {
	return gitRepoWebhookInfo{URL: strings.TrimRight(publicURL, "/") + "/api/webhooks/git/" + g.ID, Secret: g.WebhookSecret}
}

// gitFetchStatus is the HTTP status for a failed fetch from a repository:
// 404 when the path doesn't exist at the ref, 502 when the repository
// itself couldn't be reached.
func gitFetchStatus(err error) int {
	var notFound *gitsource.FileNotFoundError
	if errors.As(err, &notFound) {
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

type gitRepoRequest struct {
	Name          string  `json:"name"`
	Provider      string  `json:"provider"`
	URL           string  `json:"url"`
	Username      string  `json:"username"`
	Token         *string `json:"token,omitempty"`
	DefaultBranch string  `json:"defaultBranch"`
}

func (req gitRepoRequest) toRepo() (store.GitRepository, error) {
	g := store.GitRepository{
		Name:          strings.TrimSpace(req.Name),
		Provider:      req.Provider,
		URL:           strings.TrimSpace(req.URL),
		Username:      strings.TrimSpace(req.Username),
		DefaultBranch: strings.TrimSpace(req.DefaultBranch),
	}
	if req.Token != nil {
		g.Token = strings.TrimSpace(*req.Token)
	}
	if g.Provider == "" {
		g.Provider = gitsource.ProviderGeneric
	}
	if g.DefaultBranch == "" {
		g.DefaultBranch = "main"
	}
	switch {
	case g.Name == "":
		return g, errors.New("name is required")
	case !gitsource.ValidProvider(g.Provider):
		return g, errors.New("provider must be one of github, gitlab, azure_devops, bitbucket, generic")
	case !strings.HasPrefix(g.URL, "https://") && !strings.HasPrefix(g.URL, "http://"):
		return g, errors.New("url must be an http(s) clone URL, e.g. https://github.com/org/repo.git")
	}
	return g, nil
}

func handleListGitRepositories(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repos, err := st.ListGitRepositories(r.Context())
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		writeJSON(w, http.StatusOK, repos)
	}
}

// handleCreateGitRepository adds a repository (admin) after checking it's
// reachable with the given credentials, and returns its webhook URL and
// secret.
func handleCreateGitRepository(log *slog.Logger, st *store.Store, publicURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req gitRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		g, err := req.toRepo()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := gitsource.CheckURL(r.Context(), g.URL); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		refs, err := gitsource.ListRefs(r.Context(), toGitRepo(&g))
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach the repository: " + err.Error()})
			return
		}
		if len(refs.Branches) > 0 && !containsRef(refs.Branches, g.DefaultBranch) {
			g.DefaultBranch = refs.Branches[0].Name
		}
		if g.WebhookSecret, err = auth.RandomToken(); err != nil {
			writeActionError(w, log, err)
			return
		}
		id, err := st.CreateGitRepository(r.Context(), g, actorFromRequest(r).ID)
		if errors.Is(err, store.ErrDuplicateGitRepositoryName) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		created, err := st.GetGitRepository(r.Context(), id)
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		recordAudit(r, log, st, "git_repository.create", "git_repository", id, "Git repository "+g.Name+" added", map[string]any{"url": g.URL, "provider": g.Provider})
		writeJSON(w, http.StatusCreated, map[string]any{"repository": created, "webhook": webhookInfo(publicURL, created)})
	}
}

func containsRef(refs []gitsource.Ref, name string) bool {
	for _, r := range refs {
		if r.Name == name {
			return true
		}
	}
	return false
}

// handleUpdateGitRepository edits a repository (admin). The token is only
// replaced when the request includes one.
func handleUpdateGitRepository(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		existing, err := st.GetGitRepository(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "repository not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		var req gitRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		g, err := req.toRepo()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := gitsource.CheckURL(r.Context(), g.URL); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		g.ID = existing.ID
		replaceToken := req.Token != nil
		check := g
		if !replaceToken {
			check.Token = existing.Token
		}
		if _, err := gitsource.ListRefs(r.Context(), toGitRepo(&check)); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "couldn't reach the repository: " + err.Error()})
			return
		}
		if err := st.UpdateGitRepository(r.Context(), g, replaceToken); err != nil {
			if errors.Is(err, store.ErrDuplicateGitRepositoryName) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			writeActionError(w, log, err)
			return
		}
		recordAudit(r, log, st, "git_repository.update", "git_repository", g.ID, "Git repository "+g.Name+" updated", map[string]any{"url": g.URL, "tokenReplaced": replaceToken})
		updated, _ := st.GetGitRepository(r.Context(), g.ID)
		writeJSON(w, http.StatusOK, updated)
	}
}

func handleDeleteGitRepository(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		existing, err := st.GetGitRepository(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "repository not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		linked, err := st.ListComposeFilesByGitRepository(r.Context(), id)
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		if len(linked) > 0 {
			names := make([]string, len(linked))
			for i, f := range linked {
				names[i] = f.Name
			}
			writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf(
				"%d compose file(s) are linked to this repository: %s — unlink or delete them first",
				len(linked), strings.Join(names, ", "))})
			return
		}
		if err := st.DeleteGitRepository(r.Context(), id); err != nil {
			writeActionError(w, log, err)
			return
		}
		recordAudit(r, log, st, "git_repository.delete", "git_repository", id, "Git repository "+existing.Name+" removed", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleGetGitWebhook returns a repository's webhook URL and secret (admin).
func handleGetGitWebhook(log *slog.Logger, st *store.Store, publicURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g, err := st.GetGitRepository(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "repository not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		writeJSON(w, http.StatusOK, webhookInfo(publicURL, g))
	}
}

// handleRotateGitWebhookSecret issues a new webhook secret (admin); the old
// one stops working immediately.
func handleRotateGitWebhookSecret(log *slog.Logger, st *store.Store, publicURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		secret, err := auth.RandomToken()
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		if err := st.RotateGitWebhookSecret(r.Context(), id, secret); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "repository not found", http.StatusNotFound)
				return
			}
			writeActionError(w, log, err)
			return
		}
		g, err := st.GetGitRepository(r.Context(), id)
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		recordAudit(r, log, st, "git_repository.rotate_webhook_secret", "git_repository", id, "webhook secret rotated for "+g.Name, nil)
		writeJSON(w, http.StatusOK, webhookInfo(publicURL, g))
	}
}

func loadGitRepo(w http.ResponseWriter, r *http.Request, log *slog.Logger, st *store.Store) (*store.GitRepository, bool) {
	g, err := st.GetGitRepository(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "repository not found", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		writeActionError(w, log, err)
		return nil, false
	}
	return g, true
}

// handleListGitRefs lists a repository's branches and tags — what "Deploy
// from a selected repository branch or tag" picks from.
func handleListGitRefs(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g, ok := loadGitRepo(w, r, log, st)
		if !ok {
			return
		}
		refs, err := gitsource.ListRefs(r.Context(), toGitRepo(g))
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, refs)
	}
}

// handlePreviewGitFile fetches ?path= at ?ref= and validates it as Compose,
// so the import dialog can show what would be imported.
func handlePreviewGitFile(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g, ok := loadGitRepo(w, r, log, st)
		if !ok {
			return
		}
		ref, path := r.URL.Query().Get("ref"), r.URL.Query().Get("path")
		if ref == "" {
			ref = g.DefaultBranch
		}
		if path == "" {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}
		content, commit, err := gitsource.FetchFile(r.Context(), toGitRepo(g), ref, path)
		if err != nil {
			writeJSON(w, gitFetchStatus(err), map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"content": content, "commit": commit, "ref": ref, "parse": compose.Parse(content)})
	}
}

// handleImportGitComposeFile is "Import Compose files from Git": creates a
// Compose file from path at ref, linked to the repository so it can be
// re-synced (manually or by webhook) later.
func handleImportGitComposeFile(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Name string `json:"name"`
		Ref  string `json:"ref"`
		Path string `json:"path"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		g, ok := loadGitRepo(w, r, log, st)
		if !ok {
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		req.Name, req.Path = strings.TrimSpace(req.Name), strings.TrimPrefix(strings.TrimSpace(req.Path), "/")
		if req.Ref == "" {
			req.Ref = g.DefaultBranch
		}
		if req.Name == "" || req.Path == "" {
			http.Error(w, "name and path are required", http.StatusBadRequest)
			return
		}
		content, commit, err := gitsource.FetchFile(r.Context(), toGitRepo(g), req.Ref, req.Path)
		if err != nil {
			writeJSON(w, gitFetchStatus(err), map[string]string{"error": err.Error()})
			return
		}
		if result := compose.Parse(content); !result.Valid {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": result.Errors})
			return
		}
		id, err := st.CreateComposeFile(r.Context(), req.Name, content, actorFromRequest(r).ID)
		if errors.Is(err, store.ErrDuplicateComposeFileName) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		if err := st.SetComposeFileGitLink(r.Context(), id, &g.ID, req.Ref, req.Path); err != nil {
			writeActionError(w, log, err)
			return
		}
		if err := st.UpdateComposeFileFromGit(r.Context(), id, content, commit); err != nil {
			writeActionError(w, log, err)
			return
		}
		f, err := st.GetComposeFile(r.Context(), id)
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		recordAudit(r, log, st, "compose_file.import_git", "compose_file", id,
			fmt.Sprintf("Compose file %s imported from %s:%s@%s (%s)", req.Name, g.Name, req.Path, req.Ref, shortCommit(commit)),
			map[string]any{"repositoryId": g.ID, "ref": req.Ref, "path": req.Path, "commit": commit})
		writeJSON(w, http.StatusCreated, toComposeFileResponse(*f))
	}
}

// handleGenerateGitComposeFile creates a Git-linked Compose file for a
// repository that has a Dockerfile but no Compose file. An empty GitPath
// denotes generated content: syncing advances the commit while keeping the
// user's generated service settings.
func handleGenerateGitComposeFile(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		Name        string   `json:"name"`
		Ref         string   `json:"ref"`
		ContextPath string   `json:"contextPath"`
		Dockerfile  string   `json:"dockerfile"`
		Port        int      `json:"port"`
		EnvKeys     []string `json:"envKeys"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		g, ok := loadGitRepo(w, r, log, st)
		if !ok {
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" || len(req.Name) > 100 || req.Port < 1 || req.Port > 65535 {
			http.Error(w, "name and a port from 1 to 65535 are required", http.StatusBadRequest)
			return
		}
		if req.Ref == "" {
			req.Ref = g.DefaultBranch
		}
		if req.ContextPath == "" {
			req.ContextPath = "."
		}
		if req.Dockerfile == "" {
			req.Dockerfile = "Dockerfile"
		}
		content, dockerfilePath, err := generatedGitCompose(req.ContextPath, req.Dockerfile, req.Port, req.EnvKeys)
		if err != nil {
			http.Error(w, "invalid build context or Dockerfile path", http.StatusBadRequest)
			return
		}
		_, commit, err := gitsource.FetchFile(r.Context(), toGitRepo(g), req.Ref, dockerfilePath)
		if err != nil {
			writeJSON(w, gitFetchStatus(err), map[string]string{"error": "Dockerfile check failed: " + err.Error()})
			return
		}
		if result := compose.Parse(content); !result.Valid {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": result.Errors})
			return
		}
		id, err := st.CreateComposeFile(r.Context(), req.Name, content, actorFromRequest(r).ID)
		if errors.Is(err, store.ErrDuplicateComposeFileName) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		if err := st.SetComposeFileGitLink(r.Context(), id, &g.ID, req.Ref, ""); err != nil {
			_ = st.DeleteComposeFile(r.Context(), id)
			writeActionError(w, log, err)
			return
		}
		if err := st.UpdateComposeFileFromGit(r.Context(), id, content, commit); err != nil {
			_ = st.DeleteComposeFile(r.Context(), id)
			writeActionError(w, log, err)
			return
		}
		file, err := st.GetComposeFile(r.Context(), id)
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		recordAudit(r, log, st, "compose_file.generate_git", "compose_file", id,
			fmt.Sprintf("Compose file %s generated from %s @ %s", req.Name, g.Name, shortCommit(commit)),
			map[string]any{"repositoryId": g.ID, "ref": req.Ref, "commit": commit})
		writeJSON(w, http.StatusCreated, toComposeFileResponse(*file))
	}
}

var composeEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func generatedGitCompose(contextPath, dockerfile string, port int, envKeys []string) (content, dockerfilePath string, err error) {
	if port < 1 || port > 65535 {
		return "", "", errors.New("port out of range")
	}
	if len(envKeys) > 100 {
		return "", "", errors.New("too many environment variables")
	}
	content = fmt.Sprintf("services:\n  app:\n    build:\n      context: %s\n      dockerfile: %s\n    ports:\n      - %s\n",
		strconv.Quote(contextPath), strconv.Quote(dockerfile), strconv.Quote(fmt.Sprintf("%d:%d", port, port)))
	if len(envKeys) > 0 {
		content += "    environment:\n"
		seen := map[string]bool{}
		for _, key := range envKeys {
			if !composeEnvName.MatchString(key) || seen[key] {
				return "", "", errors.New("invalid or duplicate environment variable name")
			}
			seen[key] = true
			content += fmt.Sprintf("      %s: %s\n", key, strconv.Quote("${"+key+"}"))
		}
	}
	specs, err := compose.BuildSpecs(content, ".", nil)
	if err != nil || len(specs) != 1 {
		return "", "", errors.New("invalid build settings")
	}
	return content, path.Join(specs[0].Context, specs[0].Dockerfile), nil
}

// syncResult is the outcome of re-syncing a Git-linked Compose file.
type syncResult struct {
	Changed bool                `json:"changed"`
	Commit  string              `json:"commit"`
	File    composeFileResponse `json:"file"`
}

// syncComposeFile pulls a Git-linked file's content at its ref and saves it
// (as a new version when the content changed).
func syncComposeFile(ctx context.Context, st *store.Store, file *store.ComposeFile) (*syncResult, error) {
	if file.GitRepositoryID == nil {
		return nil, newActionError(http.StatusBadRequest, "compose file %q isn't linked to a Git repository", file.Name)
	}
	g, err := st.GetGitRepository(ctx, *file.GitRepositoryID)
	if err != nil {
		return nil, err
	}
	var content, commit string
	if file.GitPath == "" {
		content = file.Content
		commit, err = gitsource.ResolveRef(ctx, toGitRepo(g), file.GitRef)
	} else {
		content, commit, err = gitsource.FetchFile(ctx, toGitRepo(g), file.GitRef, file.GitPath)
	}
	if err != nil {
		return nil, newActionError(gitFetchStatus(err), "%v", err)
	}
	if result := compose.Parse(content); !result.Valid {
		ae := newActionError(http.StatusBadRequest, "the file at %s (%s) isn't a valid Compose file", file.GitRef, shortCommit(commit))
		ae.extra = map[string]any{"errors": result.Errors}
		return nil, ae
	}
	changed := content != file.Content
	if err := st.UpdateComposeFileFromGit(ctx, file.ID, content, commit); err != nil {
		return nil, err
	}
	updated, err := st.GetComposeFile(ctx, file.ID)
	if err != nil {
		return nil, err
	}
	return &syncResult{Changed: changed, Commit: commit, File: toComposeFileResponse(*updated)}, nil
}

// handleSyncComposeFile pulls the latest content of a Git-linked Compose
// file from its branch/tag.
func handleSyncComposeFile(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := st.GetComposeFile(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "compose file not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		res, err := syncComposeFile(r.Context(), st, f)
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		summary := fmt.Sprintf("Compose file %s synced from Git (%s)", f.Name, shortCommit(res.Commit))
		if !res.Changed {
			summary += " — no changes"
		}
		recordAudit(r, log, st, "compose_file.sync_git", "compose_file", f.ID, summary, map[string]any{"commit": res.Commit, "changed": res.Changed})
		writeJSON(w, http.StatusOK, res)
	}
}

// handleSetComposeFileGitLink links a Compose file to a repository path and
// branch/tag (or unlinks it with repositoryId null), then syncs it.
func handleSetComposeFileGitLink(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		RepositoryID *string `json:"repositoryId"`
		Ref          string  `json:"ref"`
		Path         string  `json:"path"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f, err := st.GetComposeFile(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "compose file not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.RepositoryID == nil || *req.RepositoryID == "" {
			if err := st.SetComposeFileGitLink(r.Context(), id, nil, "", ""); err != nil {
				writeActionError(w, log, err)
				return
			}
			recordAudit(r, log, st, "compose_file.unlink_git", "compose_file", id, "Compose file "+f.Name+" unlinked from Git", nil)
			updated, _ := st.GetComposeFile(r.Context(), id)
			writeJSON(w, http.StatusOK, toComposeFileResponse(*updated))
			return
		}
		req.Path = strings.TrimPrefix(strings.TrimSpace(req.Path), "/")
		if req.Ref == "" || req.Path == "" {
			http.Error(w, "ref and path are required", http.StatusBadRequest)
			return
		}
		if _, err := st.GetGitRepository(r.Context(), *req.RepositoryID); err != nil {
			http.Error(w, "repository not found", http.StatusNotFound)
			return
		}
		if err := st.SetComposeFileGitLink(r.Context(), id, req.RepositoryID, req.Ref, req.Path); err != nil {
			writeActionError(w, log, err)
			return
		}
		f, _ = st.GetComposeFile(r.Context(), id)
		res, err := syncComposeFile(r.Context(), st, f)
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		recordAudit(r, log, st, "compose_file.link_git", "compose_file", id,
			fmt.Sprintf("Compose file %s linked to %s@%s (%s)", f.Name, req.Path, req.Ref, shortCommit(res.Commit)), req)
		writeJSON(w, http.StatusOK, res.File)
	}
}

// handleListComposeFileCommits lists recent commits that changed a
// Git-linked Compose file — the choices for "Roll back to a previous
// commit". ?ref= overrides the file's own branch (for a deployment that
// tracks a different one).
func handleListComposeFileCommits(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := st.GetComposeFile(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "compose file not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		if f.GitRepositoryID == nil {
			http.Error(w, "compose file isn't linked to a Git repository", http.StatusBadRequest)
			return
		}
		g, err := st.GetGitRepository(r.Context(), *f.GitRepositoryID)
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		ref := r.URL.Query().Get("ref")
		if ref == "" {
			ref = f.GitRef
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		commits, err := gitsource.ListCommits(r.Context(), toGitRepo(g), ref, f.GitPath, limit)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, commits)
	}
}

// handleGitWebhook is "Use webhooks for automatic redeployment": an
// unauthenticated endpoint (verified by the repository's webhook secret —
// see gitsource.VerifyWebhook) that, for each pushed branch/tag, re-syncs
// the Compose files linked to it and redeploys their auto-deploy
// deployments. Redeploys go through the normal governance gate as a system
// action: a production deployment still waits for approval, and outside a
// maintenance window it's scheduled for the next one.
func handleGitWebhook(d *deployer) http.HandlerFunc {
	type redeployed struct {
		DeploymentID string `json:"deploymentId"`
		Status       string `json:"status,omitempty"`
		Error        string `json:"error,omitempty"`
	}
	type synced struct {
		ComposeFileID string `json:"composeFileId"`
		Name          string `json:"name"`
		Changed       bool   `json:"changed"`
		Commit        string `json:"commit,omitempty"`
		Error         string `json:"error,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		g, err := d.st.GetGitRepository(ctx, r.PathValue("repoId"))
		if err != nil {
			http.Error(w, "unknown repository", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		if err := gitsource.VerifyWebhook(r, body, g.WebhookSecret); err != nil {
			d.log.Warn("rejected git webhook", "repository_id", g.ID, "error", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ev, err := gitsource.ParsePushEvent(r, body)
		if err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}

		files, err := d.st.ListComposeFilesByGitRepository(ctx, g.ID)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		syncedFiles := []synced{}
		redeploys := []redeployed{}
		system := actor{Email: "webhook"}
		for _, pushed := range ev.Refs {
			for i := range files {
				f := &files[i]
				if f.GitRef == pushed.Name {
					s := synced{ComposeFileID: f.ID, Name: f.Name}
					if res, err := syncComposeFile(ctx, d.st, f); err != nil {
						s.Error = err.Error()
					} else {
						s.Changed, s.Commit = res.Changed, res.Commit
					}
					syncedFiles = append(syncedFiles, s)
					_ = d.st.RecordAudit(ctx, "", false, "compose_file.sync_git", "compose_file", f.ID,
						fmt.Sprintf("Compose file %s synced by push webhook (%s @ %s)", f.Name, pushed.Name, shortCommit(pushed.Commit)), s)
				}
				deps, err := d.st.ListAutoDeployDeployments(ctx, f.ID)
				if err != nil {
					continue
				}
				for j := range deps {
					dep := &deps[j]
					effectiveRef := dep.GitRef
					if effectiveRef == "" {
						effectiveRef = f.GitRef
					}
					if effectiveRef != pushed.Name {
						continue
					}
					rd := redeployed{DeploymentID: dep.ID}
					reason := fmt.Sprintf("push to %s (%s)", pushed.Name, shortCommit(pushed.Commit))
					outcome, err := d.submit(ctx, dep, actionRedeploy, actionParams{Reason: reason}, system, gateOptions{ScheduleForMaintenanceWindow: true}, false)
					if err != nil {
						rd.Error = err.Error()
						d.event(ctx, dep.ID, dep.Phase, "automatic redeploy on "+reason+" failed: "+err.Error(), "")
					} else {
						rd.Status = outcome.Status
					}
					redeploys = append(redeploys, rd)
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"synced": syncedFiles, "redeployed": redeploys})
	}
}
