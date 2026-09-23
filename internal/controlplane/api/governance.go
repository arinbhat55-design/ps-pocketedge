package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/gitsource"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// handleListDeploymentRequests lists approval/scheduled requests, newest
// first: ?status= (pending_approval, scheduled, executed, rejected,
// cancelled, failed) and ?deploymentId=.
func handleListDeploymentRequests(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqs, err := d.st.ListDeploymentRequests(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("deploymentId"))
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		writeJSON(w, http.StatusOK, reqs)
	}
}

type decisionRequest struct {
	Comment string `json:"comment"`
	// OverrideMaintenanceWindow runs an approved request now even outside
	// the maintenance window (instead of scheduling it for the next one).
	OverrideMaintenanceWindow bool `json:"overrideMaintenanceWindow,omitempty"`
}

// handleApproveDeploymentRequest is the approval workflow's "approve":
// admins only; the requester can't approve their own request unless the
// environment's policy allows self-approval. An approved request runs
// immediately if the environment is inside its maintenance window (or has
// none), otherwise it's scheduled for the next window.
func handleApproveDeploymentRequest(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		req, err := d.st.GetDeploymentRequest(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "request not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if req.Status != store.RequestPendingApproval {
			http.Error(w, "request is not awaiting approval (status "+req.Status+")", http.StatusConflict)
			return
		}
		var body decisionRequest
		if err := decodeOptionalBody(r, &body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		env := ""
		if req.Environment != nil {
			env = *req.Environment
		}
		policy, err := d.st.GetEnvironmentPolicy(r.Context(), env)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if req.RequestedBy != nil && *req.RequestedBy == a.ID && (policy == nil || !policy.AllowSelfApproval) {
			http.Error(w, "you can't approve your own request in this environment — another admin has to", http.StatusForbidden)
			return
		}

		now := d.now()
		scheduledFor := now
		runNow := policy.ChangeAllowedNow(now) || body.OverrideMaintenanceWindow
		if !runNow {
			next, ok := store.NextMaintenanceWindow(policy.MaintenanceWindows, now)
			if !ok {
				http.Error(w, "the environment enforces a maintenance window but has none configured", http.StatusConflict)
				return
			}
			scheduledFor = next
		}
		decided, err := d.st.DecideDeploymentRequest(r.Context(), req.ID, store.RequestScheduled, a.ID, body.Comment, &scheduledFor)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if !decided {
			http.Error(w, "request was already decided", http.StatusConflict)
			return
		}

		var p actionParams
		_ = json.Unmarshal(req.Params, &p)
		summary := describeAction(req.Action, p)
		comment := ""
		if body.Comment != "" {
			comment = ": " + body.Comment
		}
		d.audit(r.Context(), a, "deployment.approve", "deployment", req.DeploymentID, summary+" approved"+comment, map[string]any{"requestId": req.ID})
		if body.OverrideMaintenanceWindow && !policy.ChangeAllowedNow(now) {
			d.audit(r.Context(), a, "deployment.maintenance_window_override", "deployment", req.DeploymentID, summary+" approved to run outside the maintenance window", map[string]any{"requestId": req.ID})
		}

		if !runNow {
			d.event(r.Context(), req.DeploymentID, "scheduled", fmt.Sprintf("%s approved by %s%s — scheduled for the next maintenance window (%s)", summary, a.Email, comment, scheduledFor.UTC().Format(time.RFC1123)), a.ID)
			if dep, err := d.st.GetDeployment(r.Context(), req.DeploymentID); err == nil && dep.CurrentRevision == 0 {
				_ = d.st.UpdateDeploymentPhase(r.Context(), dep.ID, "scheduled")
			}
			writeJSON(w, http.StatusOK, actionOutcome{DeploymentID: req.DeploymentID, Status: store.RequestScheduled, RequestID: req.ID, ScheduledFor: &scheduledFor})
			return
		}

		d.event(r.Context(), req.DeploymentID, "pending", fmt.Sprintf("%s approved by %s%s", summary, a.Email, comment), a.ID)
		claimed, err := d.st.ClaimDeploymentRequest(r.Context(), req.ID, store.RequestScheduled)
		if err != nil || !claimed {
			writeJSON(w, http.StatusOK, actionOutcome{DeploymentID: req.DeploymentID, Status: store.RequestScheduled, RequestID: req.ID})
			return
		}
		// The requester, not the approver, is the actor of the change itself.
		requester := actor{}
		if req.RequestedBy != nil {
			requester.ID = *req.RequestedBy
		}
		outcome, err := d.runRequest(r.Context(), req, requester)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		outcome.RequestID = req.ID
		writeJSON(w, http.StatusOK, outcome)
	}
}

// handleRejectDeploymentRequest rejects a pending request (admins only). A
// brand-new deployment that was waiting on it ends up "rejected".
func handleRejectDeploymentRequest(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		var body decisionRequest
		if err := decodeOptionalBody(r, &body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		req, err := d.st.GetDeploymentRequest(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "request not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		decided, err := d.st.DecideDeploymentRequest(r.Context(), req.ID, store.RequestRejected, a.ID, body.Comment, nil)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if !decided {
			http.Error(w, "request is not awaiting approval", http.StatusConflict)
			return
		}
		d.closeRequest(r.Context(), req, a, "rejected", body.Comment)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleCancelDeploymentRequest withdraws a pending or scheduled request —
// its requester or any admin.
func handleCancelDeploymentRequest(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		var body decisionRequest
		if err := decodeOptionalBody(r, &body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		req, err := d.st.GetDeploymentRequest(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "request not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if !a.IsAdmin && (req.RequestedBy == nil || *req.RequestedBy != a.ID) {
			http.Error(w, "only the requester or an admin can cancel this request", http.StatusForbidden)
			return
		}
		cancelled, err := d.st.CancelDeploymentRequest(r.Context(), req.ID, a.ID, body.Comment)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if !cancelled {
			http.Error(w, "request is no longer pending", http.StatusConflict)
			return
		}
		d.closeRequest(r.Context(), req, a, "cancelled", body.Comment)
		w.WriteHeader(http.StatusNoContent)
	}
}

// closeRequest records a rejected/cancelled request on the deployment's
// timeline and the audit log, and settles a never-deployed deployment's
// phase.
func (d *deployer) closeRequest(ctx context.Context, req *store.DeploymentRequest, a actor, outcome, comment string) {
	var p actionParams
	_ = json.Unmarshal(req.Params, &p)
	summary := describeAction(req.Action, p)
	msg := fmt.Sprintf("%s %s by %s", summary, outcome, a.Email)
	if comment != "" {
		msg += ": " + comment
	}
	if dep, err := d.st.GetDeployment(ctx, req.DeploymentID); err == nil {
		phase := dep.Phase
		if dep.CurrentRevision == 0 {
			if open, _ := d.st.CountOpenRequests(ctx, dep.ID); open == 0 {
				phase = outcome
				_ = d.st.UpdateDeploymentPhase(ctx, dep.ID, phase)
			}
		}
		d.event(ctx, dep.ID, phase, msg, a.ID)
	}
	d.audit(ctx, a, "deployment.request_"+outcome, "deployment", req.DeploymentID, msg, map[string]any{"requestId": req.ID})
}

// handleListEnvironmentPolicies returns every environment's governance
// policy, plus whether each is currently inside a maintenance window.
func handleListEnvironmentPolicies(d *deployer) http.HandlerFunc {
	type policyResponse struct {
		store.EnvironmentPolicy
		InMaintenanceWindow bool       `json:"inMaintenanceWindow"`
		NextWindow          *time.Time `json:"nextWindow,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		policies, err := d.st.ListEnvironmentPolicies(r.Context())
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		now := d.now()
		out := make([]policyResponse, 0, len(policies))
		for _, p := range policies {
			pr := policyResponse{EnvironmentPolicy: p, InMaintenanceWindow: store.InMaintenanceWindow(p.MaintenanceWindows, now)}
			if next, ok := store.NextMaintenanceWindow(p.MaintenanceWindows, now); ok {
				pr.NextWindow = &next
			}
			out = append(out, pr)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleUpdateEnvironmentPolicy replaces one environment's policy (admin).
func handleUpdateEnvironmentPolicy(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		env := r.PathValue("environment")
		if !store.IsValidEnvironment(env) {
			http.Error(w, "environment must be one of development, test, staging, production", http.StatusBadRequest)
			return
		}
		var p store.EnvironmentPolicy
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		p.Environment = env
		for _, win := range p.MaintenanceWindows {
			if err := win.Validate(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		if p.EnforceMaintenanceWindow && len(p.MaintenanceWindows) == 0 {
			http.Error(w, "enforcing a maintenance window needs at least one window", http.StatusBadRequest)
			return
		}
		a := actorFromRequest(r)
		if err := d.st.UpsertEnvironmentPolicy(r.Context(), p, a.ID); err != nil {
			writeActionError(w, d.log, err)
			return
		}
		d.audit(r.Context(), a, "environment_policy.update", "environment", env, env+" policy updated", p)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListAuditEvents is "Complete audit trail" (admin): ?entityType=,
// ?entityId=, ?actorId=, ?q=, ?before=<id> for paging, ?limit=.
func handleListAuditEvents(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		events, err := d.st.ListAuditEvents(r.Context(), store.AuditFilter{
			EntityType: q.Get("entityType"),
			EntityID:   q.Get("entityId"),
			ActorID:    q.Get("actorId"),
			Search:     q.Get("q"),
			Before:     before,
			Limit:      limit,
		})
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		writeJSON(w, http.StatusOK, events)
	}
}

// driftReport is "Detect configuration drift": how a deployment's actual
// state differs from what it should be, along three axes.
type driftReport struct {
	CheckedAt time.Time    `json:"checkedAt"`
	Drifted   bool         `json:"drifted"`
	Config    configDrift  `json:"config"`
	Git       *gitDrift    `json:"git,omitempty"`
	Runtime   runtimeDrift `json:"runtime"`
}

// configDrift: the deployment's source (Compose file/stack) changed since
// the current revision was deployed — a redeploy would change things.
type configDrift struct {
	Drifted          bool   `json:"drifted"`
	DeployedRevision int    `json:"deployedRevision"`
	DeployedVersion  *int   `json:"deployedVersion,omitempty"`
	CurrentVersion   *int   `json:"currentVersion,omitempty"`
	Reason           string `json:"reason,omitempty"`
	DeployedContent  string `json:"deployedContent,omitempty"`
	CurrentContent   string `json:"currentContent,omitempty"`
}

// gitDrift: the tracked branch moved past the deployed commit.
type gitDrift struct {
	Drifted        bool   `json:"drifted"`
	Ref            string `json:"ref"`
	DeployedCommit string `json:"deployedCommit"`
	LatestCommit   string `json:"latestCommit,omitempty"`
	Error          string `json:"error,omitempty"`
}

// runtimeDrift: the containers actually on the server (as last reported
// by its agent) don't match what the current revision defines.
type runtimeDrift struct {
	Drifted       bool       `json:"drifted"`
	Missing       []string   `json:"missing"`
	Unexpected    []string   `json:"unexpected"`
	NotRunning    []string   `json:"notRunning"`
	ImageMismatch []string   `json:"imageMismatch"`
	InventoryAt   *time.Time `json:"inventoryAt,omitempty"`
}

// expectedContainer is one container the current revision should have.
type expectedContainer struct {
	name    string
	image   string
	oneShot bool
}

// normalizeImage makes "nginx" and "nginx:latest" compare equal.
func normalizeImage(ref string) string {
	ref = strings.TrimPrefix(ref, "docker.io/")
	ref = strings.TrimPrefix(ref, "library/")
	if strings.Contains(ref, "@") {
		return ref
	}
	last := ref[strings.LastIndex(ref, "/")+1:]
	if !strings.Contains(last, ":") {
		ref += ":latest"
	}
	return ref
}

// replicaContainerName mirrors the agent's containerName scheme.
func replicaContainerName(deploymentID, service string, replica int) string {
	if replica <= 1 {
		return "pe-" + deploymentID + "-" + service
	}
	return fmt.Sprintf("pe-%s-%s-%d", deploymentID, service, replica)
}

// compareRuntime is the pure part of runtime drift detection.
func compareRuntime(expected []expectedContainer, actual []store.FleetContainer) runtimeDrift {
	rd := runtimeDrift{Missing: []string{}, Unexpected: []string{}, NotRunning: []string{}, ImageMismatch: []string{}}
	byName := map[string]store.FleetContainer{}
	for _, c := range actual {
		byName[strings.TrimPrefix(c.Name, "/")] = c
	}
	seen := map[string]bool{}
	for _, e := range expected {
		seen[e.name] = true
		c, ok := byName[e.name]
		if !ok {
			rd.Missing = append(rd.Missing, e.name)
			continue
		}
		if c.State != "running" && !(e.oneShot && c.State == "exited") {
			rd.NotRunning = append(rd.NotRunning, fmt.Sprintf("%s (%s)", e.name, c.State))
		}
		if c.Image != "" && e.image != "" && normalizeImage(c.Image) != normalizeImage(e.image) {
			rd.ImageMismatch = append(rd.ImageMismatch, fmt.Sprintf("%s runs %s, expected %s", e.name, c.Image, e.image))
		}
	}
	for name := range byName {
		if !seen[name] {
			rd.Unexpected = append(rd.Unexpected, name)
		}
	}
	sort.Strings(rd.Missing)
	sort.Strings(rd.Unexpected)
	sort.Strings(rd.NotRunning)
	sort.Strings(rd.ImageMismatch)
	rd.Drifted = len(rd.Missing)+len(rd.Unexpected)+len(rd.NotRunning)+len(rd.ImageMismatch) > 0
	return rd
}

// handleDeploymentDrift computes the drift report for one deployment.
// The Git check reaches out to the remote, so it can take a few seconds.
func handleDeploymentDrift(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dep, ok := d.loadDeploymentOr404(w, r)
		if !ok {
			return
		}
		ctx := r.Context()
		report := driftReport{CheckedAt: d.now()}
		if dep.CurrentRevision == 0 {
			writeJSON(w, http.StatusOK, report)
			return
		}
		rev, err := d.st.GetDeploymentRevision(ctx, dep.ID, dep.CurrentRevision)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}

		// Config drift.
		report.Config = configDrift{DeployedRevision: rev.Revision, DeployedVersion: rev.ComposeVersion}
		var file *store.ComposeFile
		if dep.ComposeFileID != nil {
			if file, err = d.st.GetComposeFile(ctx, *dep.ComposeFileID); err == nil {
				v := file.Version
				report.Config.CurrentVersion = &v
				tracksOwnRef := file.GitRepositoryID != nil && dep.GitRef != "" && dep.GitRef != file.GitRef
				if !tracksOwnRef && file.Content != rev.ComposeContent {
					report.Config.Drifted = true
					report.Config.Reason = "the Compose file has changed since this revision was deployed"
					if rev.ComposeVersion != nil {
						report.Config.Reason = fmt.Sprintf("the Compose file is at version %d; revision %d deployed version %d", file.Version, rev.Revision, *rev.ComposeVersion)
					}
					report.Config.DeployedContent, report.Config.CurrentContent = rev.ComposeContent, file.Content
				}
			}
		} else if dep.StackID != nil {
			if stack, err := d.st.GetStack(ctx, *dep.StackID); err == nil && stack.ComposeYAML != rev.ComposeContent {
				report.Config.Drifted = true
				report.Config.Reason = "the catalog stack's definition has changed since this revision was deployed"
				report.Config.DeployedContent, report.Config.CurrentContent = rev.ComposeContent, stack.ComposeYAML
			}
		}
		if !report.Config.Drifted && !scalesEqual(dep.Scales, rev.Scales) {
			report.Config.Drifted = true
			report.Config.Reason = "replica counts were changed after this revision was deployed"
		}

		// Git drift.
		if file != nil && file.GitRepositoryID != nil {
			ref := dep.GitRef
			if ref == "" {
				ref = file.GitRef
			}
			gd := &gitDrift{Ref: ref, DeployedCommit: rev.GitCommit}
			if _, repo, err := d.gitRepoFor(ctx, file); err != nil {
				gd.Error = err.Error()
			} else if latest, err := gitsource.ResolveRef(ctx, repo, ref); err != nil {
				gd.Error = err.Error()
			} else {
				gd.LatestCommit = latest
				gd.Drifted = rev.GitCommit != "" && latest != rev.GitCommit
			}
			report.Git = gd
		}

		// Runtime drift.
		var expected []expectedContainer
		if project, err := loadProject(ctx, rev.ComposeContent, rev.Env); err == nil {
			for name, svc := range project.Services {
				n := 1
				if o := dep.Scales[name]; o > 0 {
					n = o
				} else if svc.Deploy != nil && svc.Deploy.Replicas != nil && *svc.Deploy.Replicas > 0 {
					n = *svc.Deploy.Replicas
				} else if svc.Scale != nil && *svc.Scale > 0 {
					n = *svc.Scale
				}
				for i := 1; i <= n; i++ {
					expected = append(expected, expectedContainer{
						name: replicaContainerName(dep.ID, name, i), image: svc.Image,
						oneShot: svc.Restart == "" || svc.Restart == "no",
					})
				}
			}
		}
		containers, err := d.st.ListContainersFiltered(ctx, store.ContainerFilter{DeploymentID: dep.ID})
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if dep.Phase != "removed" && dep.Phase != "stopped" {
			report.Runtime = compareRuntime(expected, containers)
		} else {
			report.Runtime = runtimeDrift{Missing: []string{}, Unexpected: []string{}, NotRunning: []string{}, ImageMismatch: []string{}}
		}
		for _, c := range containers {
			if report.Runtime.InventoryAt == nil || c.UpdatedAt.After(*report.Runtime.InventoryAt) {
				t := c.UpdatedAt
				report.Runtime.InventoryAt = &t
			}
		}

		report.Drifted = report.Config.Drifted || report.Runtime.Drifted || (report.Git != nil && report.Git.Drifted)
		writeJSON(w, http.StatusOK, report)
	}
}

func scalesEqual(a, b map[string]int) bool {
	norm := func(m map[string]int) map[string]int {
		out := map[string]int{}
		for k, v := range m {
			if v > 0 {
				out[k] = v
			}
		}
		return out
	}
	na, nb := norm(a), norm(b)
	if len(na) != len(nb) {
		return false
	}
	for k, v := range na {
		if nb[k] != v {
			return false
		}
	}
	return true
}

// governanceWorkerInterval is how often the worker looks for due scheduled
// requests and failed health verifications needing automatic rollback.
const governanceWorkerInterval = 20 * time.Second

// RunGovernanceWorker runs until ctx is done:
//   - executes approved/scheduled requests whose maintenance window has
//     arrived (re-checking the window, since the policy may have changed);
//   - performs automatic rollback for deployments with auto-rollback on
//     whose current revision failed post-deployment health verification,
//     back to the last healthy revision.
func (d *deployer) RunGovernanceWorker(ctx context.Context) {
	ticker := time.NewTicker(governanceWorkerInterval)
	defer ticker.Stop()
	for {
		d.runDueRequests(ctx)
		d.runAutoRollbacks(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *deployer) runDueRequests(ctx context.Context) {
	now := d.now()
	due, err := d.st.ListDueScheduledRequests(ctx, now)
	if err != nil {
		d.log.Error("failed to list scheduled deployment requests", "error", err)
		return
	}
	for i := range due {
		req := &due[i]
		env := ""
		if req.Environment != nil {
			env = *req.Environment
		}
		policy, err := d.st.GetEnvironmentPolicy(ctx, env)
		if err != nil {
			continue
		}
		// An approval with an explicit override was scheduled for "now"
		// by the approver; only a window-scheduled request is re-checked.
		overridden := req.DecidedAt != nil && req.ScheduledFor != nil && !req.ScheduledFor.After(req.DecidedAt.Add(time.Second))
		if !overridden && !policy.ChangeAllowedNow(now) {
			if next, ok := store.NextMaintenanceWindow(policy.MaintenanceWindows, now); ok {
				_ = d.st.RescheduleDeploymentRequest(ctx, req.ID, next)
			}
			continue
		}
		claimed, err := d.st.ClaimDeploymentRequest(ctx, req.ID, store.RequestScheduled)
		if err != nil || !claimed {
			continue
		}
		requester := actor{}
		if req.RequestedBy != nil {
			requester.ID = *req.RequestedBy
		}
		d.log.Info("running scheduled deployment request", "request_id", req.ID, "deployment_id", req.DeploymentID, "action", req.Action)
		if _, err := d.runRequest(ctx, req, requester); err != nil {
			d.log.Warn("scheduled deployment request failed", "request_id", req.ID, "error", err)
		}
	}
}

func (d *deployer) runAutoRollbacks(ctx context.Context) {
	pending, err := d.st.ListPendingAutoRollbacks(ctx)
	if err != nil {
		d.log.Error("failed to list pending auto-rollbacks", "error", err)
		return
	}
	for _, p := range pending {
		ok, err := d.st.MarkAutoRollbackAttempted(ctx, p.DeploymentID, p.Revision)
		if err != nil || !ok {
			continue
		}
		dep, err := d.st.GetDeployment(ctx, p.DeploymentID)
		if err != nil {
			continue
		}
		target, err := d.st.LastGoodRevision(ctx, dep.ID, p.Revision)
		if err != nil {
			d.event(ctx, dep.ID, dep.Phase, fmt.Sprintf("automatic rollback skipped: no earlier healthy revision to roll back to from revision %d", p.Revision), "")
			continue
		}
		reason := fmt.Sprintf("automatic rollback after revision %d failed health verification", p.Revision)
		d.log.Info("auto-rolling back deployment", "deployment_id", dep.ID, "from", p.Revision, "to", target.Revision)
		// Auto-rollback is itself the safety mechanism, so it bypasses the
		// approval/maintenance-window gate.
		if _, err := d.execute(ctx, dep, actionRollback, actionParams{Revision: target.Revision, Reason: reason}, actor{}); err != nil {
			d.event(ctx, dep.ID, dep.Phase, "automatic rollback failed: "+err.Error(), "")
		}
	}
}

// RunGovernanceWorker is the exported entry point main uses to start the
// background worker (see deployer.RunGovernanceWorker).
func RunGovernanceWorker(ctx context.Context, log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus, opWaiter *deploy.OpWaiter) {
	newDeployer(log, st, dispatcher, events, opWaiter).RunGovernanceWorker(ctx)
}
