package api

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/gitsource"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// The Git report is the fleet-wide version of the drift report's Git
// check: for every deployment whose Compose file is linked to Git, whether
// its tracked branch has commits that aren't deployed yet.

// Git report statuses, in the order the report lists them.
const (
	gitStatusBehind      = "behind"       // the branch has newer commits
	gitStatusPending     = "pending"      // behind, but a change is already waiting for approval or a maintenance window
	gitStatusError       = "error"        // the repository couldn't be reached
	gitStatusNotDeployed = "not_deployed" // no rollout has recorded a commit yet
	gitStatusUpToDate    = "up_to_date"
)

var gitStatusOrder = map[string]int{
	gitStatusBehind: 0, gitStatusPending: 1, gitStatusError: 2, gitStatusNotDeployed: 3, gitStatusUpToDate: 4,
}

// gitReportConcurrency bounds how many repositories/branches are checked
// at once.
const gitReportConcurrency = 4

// gitReportRow is one deployment's Git status; rows with the same
// ComposeFileID are the same app deployed to different environments. DeployedAt is when its
// current revision was rolled out; the *Info fields and CommitsBehind come
// from the repository's history (DetailError says why they're missing).
type gitReportRow struct {
	DeploymentID       string             `json:"deploymentId"`
	ComposeFileID      string             `json:"composeFileId"`
	SourceName         string             `json:"sourceName"`
	ServerName         string             `json:"serverName"`
	Environment        string             `json:"environment,omitempty"`
	Phase              string             `json:"phase"`
	Repository         string             `json:"repository"`
	Ref                string             `json:"ref"`
	Status             string             `json:"status"`
	PendingRequests    int                `json:"pendingRequests"`
	DeployedCommit     string             `json:"deployedCommit,omitempty"`
	DeployedAt         *time.Time         `json:"deployedAt,omitempty"`
	LatestCommit       string             `json:"latestCommit,omitempty"`
	DeployedCommitInfo *gitsource.Commit  `json:"deployedCommitInfo,omitempty"`
	LatestCommitInfo   *gitsource.Commit  `json:"latestCommitInfo,omitempty"`
	CommitsBehind      []gitsource.Commit `json:"commitsBehind,omitempty"`
	MoreCommitsBehind  bool               `json:"moreCommitsBehind,omitempty"`
	DetailError        string             `json:"detailError,omitempty"`
	Error              string             `json:"error,omitempty"`
}

type gitReport struct {
	CheckedAt   time.Time      `json:"checkedAt"`
	Deployments []gitReportRow `json:"deployments"`
}

func gitReportStatus(deployed, latest string, pendingRequests int) string {
	switch {
	case deployed == "":
		return gitStatusNotDeployed
	case deployed == latest:
		return gitStatusUpToDate
	case pendingRequests > 0:
		return gitStatusPending
	default:
		return gitStatusBehind
	}
}

// handleDeploymentGitReport builds the Git report. Each repository/branch
// is asked for its tip once, and cloned once when a deployment tracking
// it is behind, however many deployments track it.
func handleDeploymentGitReport(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		deps, err := d.st.ListDeployments(ctx, store.DeploymentFilter{Limit: 500})
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		files, err := d.st.ListComposeFiles(ctx)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		repos, err := d.st.ListGitRepositories(ctx)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		fileByID := map[string]*store.ComposeFile{}
		for i := range files {
			fileByID[files[i].ID] = &files[i]
		}
		repoByID := map[string]*store.GitRepository{}
		for i := range repos {
			repoByID[repos[i].ID] = &repos[i]
		}

		type branch struct{ repoID, ref string }
		report := gitReport{CheckedAt: d.now(), Deployments: []gitReportRow{}}
		groups := map[branch][]int{}
		for _, dep := range deps {
			if dep.ComposeFileID == nil {
				continue
			}
			file := fileByID[*dep.ComposeFileID]
			if file == nil || file.GitRepositoryID == nil || repoByID[*file.GitRepositoryID] == nil {
				continue
			}
			ref := dep.GitRef
			if ref == "" {
				ref = file.GitRef
			}
			row := gitReportRow{
				DeploymentID: dep.ID, ComposeFileID: file.ID, SourceName: dep.SourceName, ServerName: dep.ServerName, Phase: dep.Phase,
				Repository: repoByID[*file.GitRepositoryID].Name, Ref: ref, PendingRequests: dep.PendingRequests,
			}
			if dep.DeployEnvironment != nil {
				row.Environment = *dep.DeployEnvironment
			}
			if dep.CurrentRevision > 0 {
				row.DeployedCommit, row.DeployedAt = dep.LatestGitCommit, dep.LatestRevisionAt
			}
			key := branch{*file.GitRepositoryID, ref}
			groups[key] = append(groups[key], len(report.Deployments))
			report.Deployments = append(report.Deployments, row)
		}

		sem := make(chan struct{}, gitReportConcurrency)
		var wg sync.WaitGroup
		for key, rows := range groups {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				group := make([]*gitReportRow, len(rows))
				for i, n := range rows {
					group[i] = &report.Deployments[n]
				}
				checkGitBranch(ctx, toGitRepo(repoByID[key.repoID]), key.ref, group)
			}()
		}
		wg.Wait()

		sort.SliceStable(report.Deployments, func(i, j int) bool {
			a, b := report.Deployments[i], report.Deployments[j]
			if gitStatusOrder[a.Status] != gitStatusOrder[b.Status] {
				return gitStatusOrder[a.Status] < gitStatusOrder[b.Status]
			}
			return a.SourceName < b.SourceName
		})
		writeJSON(w, http.StatusOK, report)
	}
}

// checkGitBranch fills in the Git status of rows, which all track ref in
// repo.
func checkGitBranch(ctx context.Context, repo gitsource.Repo, ref string, rows []*gitReportRow) {
	latest, err := gitsource.ResolveRef(ctx, repo, ref)
	if err != nil {
		for _, row := range rows {
			row.Status, row.Error = gitStatusError, err.Error()
		}
		return
	}
	deployed := make([]string, len(rows))
	for i, row := range rows {
		deployed[i] = row.DeployedCommit
	}
	cmps, cmpErr := gitsource.CompareCommitsMany(ctx, repo, ref, latest, deployed, maxDriftCommits)
	for _, row := range rows {
		row.LatestCommit = latest
		row.Status = gitReportStatus(row.DeployedCommit, latest, row.PendingRequests)
		if cmpErr != nil {
			row.DetailError = cmpErr.Error()
			continue
		}
		cmp := cmps[row.DeployedCommit]
		row.LatestCommitInfo, row.DeployedCommitInfo = &cmp.Latest, cmp.Deployed
		row.CommitsBehind, row.MoreCommitsBehind = cmp.Behind, cmp.More
	}
}
