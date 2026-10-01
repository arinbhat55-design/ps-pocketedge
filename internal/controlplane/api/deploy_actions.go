package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"time"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/compose"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/gitsource"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// Deployment actions that go through the governance gate (environment
// policy: approval, maintenance window) and, once allowed, execute.
const (
	actionDeploy              = "deploy"
	actionRedeploy            = "redeploy"
	actionRollback            = "rollback"
	actionScale               = "scale"
	actionRedeployService     = "redeploy_service"
	actionDatabaseReconfigure = "database_reconfigure"
	actionDatabaseMigration   = "database_migration"
)

// maxReplicas bounds a scale request — a typo shouldn't start 5000
// containers on an edge box.
const maxReplicas = 50

// actionParams is what an action needs beyond the deployment itself. It's
// stored verbatim in deployment_requests.params when the action is queued,
// so it must round-trip through JSON.
type actionParams struct {
	// Rollback target — exactly one of these.
	Revision  int    `json:"revision,omitempty"`
	VersionID string `json:"versionId,omitempty"`
	GitCommit string `json:"gitCommit,omitempty"`
	// Scale / redeploy_service.
	Service  string `json:"service,omitempty"`
	Replicas int    `json:"replicas,omitempty"`
	// Promotion: the first deploy of a promoted deployment runs exactly
	// what the source deployment's revision ran.
	FromDeploymentID string `json:"fromDeploymentId,omitempty"`
	FromRevision     int    `json:"fromRevision,omitempty"`
	// Automatic actions (auto-rollback, webhook) note why they ran.
	Reason            string                   `json:"reason,omitempty"`
	DatabaseConfig    *databaseConfigParams    `json:"databaseConfig,omitempty"`
	DatabaseMigration *databaseMigrationParams `json:"databaseMigration,omitempty"`
}

type databaseMigrationParams struct {
	TargetID string `json:"targetId"`
	BackupID string `json:"backupId"`
}

type databaseConfigParams struct {
	InstanceID             string  `json:"instanceId"`
	Version                string  `json:"version"`
	MemoryMB               int     `json:"memoryMb"`
	CPUs                   float64 `json:"cpus"`
	StorageGB              int     `json:"storageGb"`
	ComposeYAML            string  `json:"composeYaml"`
	ExpectedComposeVersion int     `json:"expectedComposeVersion"`
}

// actionError carries the HTTP status an action failure should map to.
type actionError struct {
	status int
	msg    string
	extra  map[string]any
}

func (e *actionError) Error() string { return e.msg }

func newActionError(status int, format string, args ...any) *actionError {
	return &actionError{status: status, msg: fmt.Sprintf(format, args...)}
}

// writeActionError writes err as JSON {"error": ...}, using actionError's
// status when it is one.
func writeActionError(w http.ResponseWriter, log *slog.Logger, err error) {
	var ae *actionError
	if errors.As(err, &ae) {
		body := map[string]any{"error": ae.msg}
		for k, v := range ae.extra {
			body[k] = v
		}
		writeJSON(w, ae.status, body)
		return
	}
	log.Error("deployment action failed", "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

// actionOutcome is what running (or queueing) an action produced.
type actionOutcome struct {
	DeploymentID string     `json:"deploymentId"`
	Status       string     `json:"status"` // dispatched, building, completed, pending_approval, scheduled
	RequestID    string     `json:"requestId,omitempty"`
	ScheduledFor *time.Time `json:"scheduledFor,omitempty"`
	Revision     int        `json:"revision,omitempty"`
	Message      string     `json:"message,omitempty"`
	// For synchronous actions (scale, redeploy_service).
	Success *bool  `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

// gateOptions are the caller's choices when a policy would block an action.
type gateOptions struct {
	// OverrideMaintenanceWindow runs the action now even outside the
	// window. Admin only, and audited.
	OverrideMaintenanceWindow bool `json:"overrideMaintenanceWindow,omitempty"`
	// ScheduleForMaintenanceWindow queues the action for the next window
	// instead of failing when outside one.
	ScheduleForMaintenanceWindow bool `json:"scheduleForMaintenanceWindow,omitempty"`
}

// actor is who's performing an action. ID is empty for system actions (a
// webhook, the scheduler, auto-rollback).
type actor struct {
	ID      string
	Email   string
	IsAdmin bool
	// Local is a no-login local session borrowing an admin account.
	Local bool
}

func actorFromRequest(r *http.Request) actor {
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		email := claims.Email
		if claims.Local {
			// Local sessions borrow an admin account; say so in audit and
			// event messages so they aren't mistaken for that admin.
			email += " (local session)"
		}
		return actor{ID: claims.UserID, Email: email, IsAdmin: claims.Role == "admin", Local: claims.Local}
	}
	return actor{}
}

// deployer runs deployment actions: resolving what to deploy, enforcing
// environment policy, recording revisions and audit events, and
// dispatching to the agent.
type deployer struct {
	log        *slog.Logger
	st         *store.Store
	dispatcher *deploy.Dispatcher
	events     *deploy.EventBus
	opWaiter   *deploy.OpWaiter
	builds     *deploy.BuildBus
	publicURL  string
	now        func() time.Time
}

func newDeployer(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus, opWaiter *deploy.OpWaiter, builds *deploy.BuildBus) *deployer {
	return &deployer{log: log, st: st, dispatcher: dispatcher, events: events, opWaiter: opWaiter, builds: builds, now: time.Now}
}

func (d *deployer) event(ctx context.Context, deploymentID, phase, message, actorID string) {
	if e, err := d.st.AddDeploymentEvent(ctx, deploymentID, phase, message, actorID); err == nil {
		d.events.Publish(deploymentID, e)
	} else {
		d.log.Error("failed to record deployment event", "deployment_id", deploymentID, "error", err)
	}
}

func (d *deployer) audit(ctx context.Context, a actor, action, entityType, entityID, summary string, details any) {
	if err := d.st.RecordAudit(ctx, a.ID, a.Local, action, entityType, entityID, summary, details); err != nil {
		d.log.Error("failed to record audit event", "action", action, "error", err)
	}
}

func deploymentEnvironment(dep *store.Deployment) string {
	if dep.DeployEnvironment == nil {
		return ""
	}
	return *dep.DeployEnvironment
}

// describeAction is a human summary for events, requests, and the audit log.
func describeAction(action string, p actionParams) string {
	switch action {
	case actionDeploy:
		if p.FromDeploymentID != "" {
			return fmt.Sprintf("deploy (promoted from revision %d of another deployment)", p.FromRevision)
		}
		return "deploy"
	case actionRedeploy:
		return "redeploy"
	case actionRollback:
		switch {
		case p.Revision > 0:
			return fmt.Sprintf("rollback to revision %d", p.Revision)
		case p.GitCommit != "":
			return "rollback to commit " + shortCommit(p.GitCommit)
		default:
			return "rollback to an earlier Compose file version"
		}
	case actionScale:
		return fmt.Sprintf("scale service %q to %d replica(s)", p.Service, p.Replicas)
	case actionRedeployService:
		return fmt.Sprintf("redeploy service %q", p.Service)
	case actionDatabaseReconfigure:
		return "change PostgreSQL version or resources"
	case actionDatabaseMigration:
		return "migrate PostgreSQL data from a logical backup"
	}
	return action
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

// checkPolicyRequirements enforces an environment's required-metadata
// rules. Rollbacks are exempt from needing a rollback plan (they are the
// rollback plan).
func checkPolicyRequirements(policy *store.EnvironmentPolicy, action, changeRequest, rollbackPlan string) error {
	if policy == nil {
		return nil
	}
	if policy.RequireChangeRequest && changeRequest == "" {
		return newActionError(http.StatusBadRequest, "the %s environment requires a change request reference", policy.Environment)
	}
	if policy.RequireRollbackPlan && rollbackPlan == "" && action != actionRollback {
		return newActionError(http.StatusBadRequest, "the %s environment requires a rollback plan", policy.Environment)
	}
	return nil
}

// submit runs action against dep through the governance gate:
//
//  1. required metadata (change request, rollback plan) is checked;
//  2. if the environment requires approval, a pending_approval request is
//     queued (and a brand-new deployment is parked as awaiting_approval);
//  3. otherwise, outside an enforced maintenance window, the action is
//     either queued for the next window (opts.ScheduleForMaintenanceWindow),
//     run anyway (opts.OverrideMaintenanceWindow, admins only), or
//     rejected with the next window's start time;
//  4. otherwise it runs now.
//
// isNew is true for a deployment's first deploy, which hasn't been
// dispatched yet — its phase reflects the queued state.
func (d *deployer) submit(ctx context.Context, dep *store.Deployment, action string, params actionParams, a actor, opts gateOptions, isNew bool) (*actionOutcome, error) {
	policy, err := d.st.GetEnvironmentPolicy(ctx, deploymentEnvironment(dep))
	if err != nil {
		return nil, err
	}
	if err := checkPolicyRequirements(policy, action, dep.ChangeRequest, dep.RollbackPlan); err != nil {
		return nil, err
	}
	summary := describeAction(action, params)

	if policy != nil && policy.RequireApproval {
		reason := fmt.Sprintf("the %s environment requires approval", policy.Environment)
		id, err := d.st.CreateDeploymentRequest(ctx, dep.ID, action, params, store.RequestPendingApproval, reason, a.ID, nil)
		if err != nil {
			return nil, err
		}
		if isNew {
			_ = d.st.UpdateDeploymentPhase(ctx, dep.ID, "awaiting_approval")
		}
		d.event(ctx, dep.ID, "awaiting_approval", summary+" requested — waiting for approval", a.ID)
		d.audit(ctx, a, "deployment.request_approval", "deployment", dep.ID, summary+" awaiting approval", map[string]any{"requestId": id, "action": action, "params": params})
		return &actionOutcome{DeploymentID: dep.ID, Status: store.RequestPendingApproval, RequestID: id, Message: reason}, nil
	}

	now := d.now()
	if !policy.ChangeAllowedNow(now) {
		next, hasNext := store.NextMaintenanceWindow(policy.MaintenanceWindows, now)
		switch {
		case opts.OverrideMaintenanceWindow && a.IsAdmin:
			d.audit(ctx, a, "deployment.maintenance_window_override", "deployment", dep.ID, summary+" run outside the maintenance window", map[string]any{"action": action})
		case opts.ScheduleForMaintenanceWindow && hasNext:
			reason := fmt.Sprintf("outside the %s maintenance window", policy.Environment)
			id, err := d.st.CreateDeploymentRequest(ctx, dep.ID, action, params, store.RequestScheduled, reason, a.ID, &next)
			if err != nil {
				return nil, err
			}
			if isNew {
				_ = d.st.UpdateDeploymentPhase(ctx, dep.ID, "scheduled")
			}
			d.event(ctx, dep.ID, "scheduled", fmt.Sprintf("%s scheduled for the next maintenance window (%s)", summary, next.UTC().Format(time.RFC1123)), a.ID)
			d.audit(ctx, a, "deployment.schedule", "deployment", dep.ID, summary+" scheduled for the maintenance window", map[string]any{"requestId": id, "scheduledFor": next})
			return &actionOutcome{DeploymentID: dep.ID, Status: store.RequestScheduled, RequestID: id, ScheduledFor: &next, Message: reason}, nil
		default:
			ae := newActionError(http.StatusConflict, "the %s environment only allows changes during its maintenance window", policy.Environment)
			ae.extra = map[string]any{"outsideMaintenanceWindow": true, "canOverride": a.IsAdmin}
			if hasNext {
				ae.extra["nextWindow"] = next
			}
			return nil, ae
		}
	}

	return d.execute(ctx, dep, action, params, a)
}

// precheckMaintenanceWindow returns the 409 submit would return for a new
// deployment in policy's environment, so create/promote can fail before
// inserting a row the caller would then retry around (creating a
// duplicate). Approval-gated environments are queued rather than blocked,
// so they pass.
func (d *deployer) precheckMaintenanceWindow(policy *store.EnvironmentPolicy, a actor, opts gateOptions) error {
	if policy == nil || policy.RequireApproval {
		return nil
	}
	now := d.now()
	if policy.ChangeAllowedNow(now) || (opts.OverrideMaintenanceWindow && a.IsAdmin) {
		return nil
	}
	next, hasNext := store.NextMaintenanceWindow(policy.MaintenanceWindows, now)
	if opts.ScheduleForMaintenanceWindow && hasNext {
		return nil
	}
	ae := newActionError(http.StatusConflict, "the %s environment only allows changes during its maintenance window", policy.Environment)
	ae.extra = map[string]any{"outsideMaintenanceWindow": true, "canOverride": a.IsAdmin}
	if hasNext {
		ae.extra["nextWindow"] = next
	}
	return ae
}

// rollout is everything a full-stack dispatch needs.
type rollout struct {
	name           string
	content        string
	env            map[string]string
	scales         map[string]int
	composeVersion *int
	gitRef         string
	gitCommit      string
	// sourceContent is content as written, when content had its build:
	// sections replaced by built image tags (see planBuilds).
	sourceContent string
}

// gitRepoFor returns the gitsource.Repo a Git-linked Compose file came from.
func (d *deployer) gitRepoFor(ctx context.Context, file *store.ComposeFile) (*store.GitRepository, gitsource.Repo, error) {
	if file.GitRepositoryID == nil {
		return nil, gitsource.Repo{}, newActionError(http.StatusBadRequest, "compose file %q isn't linked to a Git repository", file.Name)
	}
	repo, err := d.st.GetGitRepository(ctx, *file.GitRepositoryID)
	if err != nil {
		return nil, gitsource.Repo{}, err
	}
	return repo, gitsource.Repo{URL: repo.URL, Provider: repo.Provider, Username: repo.Username, Token: repo.Token}, nil
}

// resolveRollout decides exactly what a deploy/redeploy/rollback will run.
func (d *deployer) resolveRollout(ctx context.Context, dep *store.Deployment, action string, p actionParams) (*rollout, error) {
	ro := &rollout{env: dep.Env, scales: dep.Scales}

	if action == actionRollback {
		switch {
		case p.Revision > 0:
			rev, err := d.st.GetDeploymentRevision(ctx, dep.ID, p.Revision)
			if errors.Is(err, store.ErrNotFound) {
				return nil, newActionError(http.StatusNotFound, "revision %d not found", p.Revision)
			}
			if err != nil {
				return nil, err
			}
			name, _, err := d.st.ResolveDeploymentSource(ctx, dep)
			if err != nil {
				return nil, err
			}
			ro.name, ro.content, ro.env, ro.scales = name, rev.ComposeContent, rev.Env, rev.Scales
			ro.composeVersion, ro.gitRef, ro.gitCommit, ro.sourceContent = rev.ComposeVersion, rev.GitRef, rev.GitCommit, rev.SourceContent
			return ro, nil
		case p.VersionID != "":
			if dep.ComposeFileID == nil {
				return nil, newActionError(http.StatusBadRequest, "rollback to a Compose file version is only supported for deployments sourced from a Compose file")
			}
			v, err := d.st.GetComposeFileVersion(ctx, *dep.ComposeFileID, p.VersionID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, newActionError(http.StatusNotFound, "version not found")
			}
			if err != nil {
				return nil, err
			}
			n := v.VersionNumber
			ro.name, ro.content, ro.composeVersion, ro.gitCommit = v.Name, v.Content, &n, v.GitCommit
			return ro, nil
		case p.GitCommit != "":
			if dep.ComposeFileID == nil {
				return nil, newActionError(http.StatusBadRequest, "rollback to a commit is only supported for Git-linked Compose files")
			}
			file, err := d.st.GetComposeFile(ctx, *dep.ComposeFileID)
			if err != nil {
				return nil, err
			}
			_, repo, err := d.gitRepoFor(ctx, file)
			if err != nil {
				return nil, err
			}
			ref := dep.GitRef
			if ref == "" {
				ref = file.GitRef
			}
			var content, commit string
			if file.GitPath == "" {
				content = file.Content
				// Verify the commit belongs to this repository before building it.
				specs, specErr := compose.BuildSpecs(content, ".", ro.env)
				if specErr != nil || len(specs) == 0 {
					return nil, newActionError(http.StatusBadRequest, "generated Compose file has no valid build")
				}
				_, commit, err = gitsource.FetchFileAtCommit(ctx, repo, ref, p.GitCommit, path.Join(specs[0].Context, specs[0].Dockerfile))
			} else {
				content, commit, err = gitsource.FetchFileAtCommit(ctx, repo, ref, p.GitCommit, file.GitPath)
			}
			if err != nil {
				return nil, newActionError(http.StatusBadGateway, "%v", err)
			}
			ro.name, ro.content, ro.gitRef, ro.gitCommit = file.Name, content, ref, commit
			return ro, nil
		}
		return nil, newActionError(http.StatusBadRequest, "rollback needs a revision, versionId, or gitCommit")
	}

	if p.FromDeploymentID != "" {
		rev, err := d.st.GetDeploymentRevision(ctx, p.FromDeploymentID, p.FromRevision)
		if err != nil {
			return nil, newActionError(http.StatusNotFound, "source revision for promotion not found")
		}
		name, _, err := d.st.ResolveDeploymentSource(ctx, dep)
		if err != nil {
			return nil, err
		}
		ro.name, ro.content, ro.composeVersion, ro.gitRef, ro.gitCommit = name, rev.ComposeContent, rev.ComposeVersion, rev.GitRef, rev.GitCommit
		ro.sourceContent = rev.SourceContent
		if len(ro.scales) == 0 {
			ro.scales = rev.Scales
		}
		return ro, nil
	}

	if dep.StackID != nil {
		stack, err := d.st.GetStack(ctx, *dep.StackID)
		if err != nil {
			return nil, err
		}
		ro.name, ro.content = stack.Name, stack.ComposeYAML
		return ro, nil
	}
	if dep.ComposeFileID == nil {
		return nil, errors.New("deployment has no source")
	}
	file, err := d.st.GetComposeFile(ctx, *dep.ComposeFileID)
	if err != nil {
		return nil, err
	}
	ro.name = file.Name
	if file.GitRepositoryID != nil && file.GitPath == "" {
		_, repo, err := d.gitRepoFor(ctx, file)
		if err != nil {
			return nil, err
		}
		ref := dep.GitRef
		if ref == "" {
			ref = file.GitRef
		}
		commit, err := gitsource.ResolveRef(ctx, repo, ref)
		if err != nil {
			return nil, newActionError(http.StatusBadGateway, "%v", err)
		}
		v := file.Version
		ro.content, ro.composeVersion, ro.gitRef, ro.gitCommit = file.Content, &v, ref, commit
		return ro, nil
	}
	// A Git-linked deployment that tracks its own branch/tag (not the
	// file's) deploys that ref's latest commit, fetched fresh.
	if file.GitRepositoryID != nil && dep.GitRef != "" && dep.GitRef != file.GitRef {
		_, repo, err := d.gitRepoFor(ctx, file)
		if err != nil {
			return nil, err
		}
		var content, commit string
		if file.GitPath == "" {
			content = file.Content
			commit, err = gitsource.ResolveRef(ctx, repo, dep.GitRef)
		} else {
			content, commit, err = gitsource.FetchFile(ctx, repo, dep.GitRef, file.GitPath)
		}
		if err != nil {
			return nil, newActionError(http.StatusBadGateway, "%v", err)
		}
		ro.content, ro.gitRef, ro.gitCommit = content, dep.GitRef, commit
		return ro, nil
	}
	v := file.Version
	ro.content, ro.composeVersion, ro.gitRef, ro.gitCommit = file.Content, &v, file.GitRef, file.GitCommit
	return ro, nil
}

// execute runs an action that the gate has allowed.
func (d *deployer) execute(ctx context.Context, dep *store.Deployment, action string, p actionParams, a actor) (*actionOutcome, error) {
	switch action {
	case actionDeploy, actionRedeploy, actionRollback:
		return d.executeRollout(ctx, dep, action, p, a)
	case actionScale:
		return d.executeScale(ctx, dep, p, a)
	case actionRedeployService:
		return d.executeServiceRedeploy(ctx, dep, p, a)
	case actionDatabaseReconfigure:
		return d.executeDatabaseReconfigure(ctx, dep, p, a)
	case actionDatabaseMigration:
		return d.executeDatabaseMigration(ctx, dep, p, a)
	}
	return nil, newActionError(http.StatusBadRequest, "unknown action %q", action)
}

func (d *deployer) executeRollout(ctx context.Context, dep *store.Deployment, action string, p actionParams, a actor) (*actionOutcome, error) {
	ro, err := d.resolveRollout(ctx, dep, action, p)
	if err != nil {
		return nil, err
	}
	jobs, err := d.planBuilds(ctx, dep, ro)
	if err != nil {
		return nil, err
	}
	if len(jobs) > 0 && a.ID != "" && !a.IsAdmin {
		return nil, newActionError(http.StatusForbidden, "only admins can deploy services with build:")
	}
	if images, err := composeImages(ctx, ro.content, ro.env); err == nil {
		trustedTags := make([]string, 0, len(jobs))
		for _, job := range jobs {
			trustedTags = append(trustedTags, job.tag)
		}
		if err := enforceImagePolicy(ctx, d.st, images, trustedTags...); err != nil {
			return nil, newActionError(http.StatusForbidden, "%v", err)
		}
	}
	changes := d.describeChanges(ctx, dep, ro)
	revision, err := d.st.RecordRevision(ctx, store.NewRevision{
		DeploymentID:   dep.ID,
		Action:         action,
		ComposeContent: ro.content,
		Env:            ro.env,
		Scales:         ro.scales,
		ComposeVersion: ro.composeVersion,
		GitRef:         ro.gitRef,
		GitCommit:      ro.gitCommit,
		SourceContent:  ro.sourceContent,
		ChangeSummary:  changes,
		Strategy:       dep.UpdateStrategy,
		CreatedBy:      a.ID,
	})
	if err != nil {
		return nil, err
	}
	phase := "pending"
	if len(jobs) > 0 {
		phase = "building"
	}
	_ = d.st.UpdateDeploymentPhase(ctx, dep.ID, phase)

	summary := describeAction(action, p)
	message := fmt.Sprintf("%s — revision %d", summary, revision)
	if ro.gitCommit != "" {
		message += ", commit " + shortCommit(ro.gitCommit)
	}
	if dep.UpdateStrategy == "rolling" && action != actionDeploy {
		message += " (rolling update)"
	}
	if p.Reason != "" {
		message += " — " + p.Reason
	}
	if changes != "" {
		message += " — changes: " + changes
	}
	d.event(ctx, dep.ID, phase, message, a.ID)
	d.audit(ctx, a, "deployment."+action, "deployment", dep.ID, message, map[string]any{
		"revision": revision, "gitCommit": ro.gitCommit, "composeVersion": ro.composeVersion, "params": p,
	})
	d.supersedeBuilds(ctx, dep.ID, revision)

	if len(jobs) > 0 {
		// Building takes minutes; the deploy follows once the images are
		// ready, reported on the deployment's event stream like any other
		// progress.
		go d.buildThenDeploy(dep, ro, jobs, revision, a)
		return &actionOutcome{DeploymentID: dep.ID, Status: "building", Revision: revision, Message: message}, nil
	}

	if err := dispatchDeploy(ctx, d.log, d.st, d.dispatcher, d.events, dep.ID, dep.ServerID, ro.name, ro.content, ro.env, ro.scales, dep.UpdateStrategy, revision); err != nil {
		// Nothing reached the server, so whatever revision was running
		// before is still what's running.
		_ = d.st.UpdateCurrentRevisionStatus(ctx, dep.ID, "failed", "server not connected")
		_ = d.st.RevertToPreviousRevision(ctx, dep.ID)
		ae := newActionError(http.StatusConflict, "server not connected")
		ae.extra = map[string]any{"deploymentId": dep.ID}
		return nil, ae
	}
	return &actionOutcome{DeploymentID: dep.ID, Status: "dispatched", Revision: revision, Message: message}, nil
}

// currentContent is what the deployment is running now: its current
// revision's content, or (for deployments from before revisions existed)
// its source's content.
func (d *deployer) currentContent(ctx context.Context, dep *store.Deployment) (name, content string, env map[string]string, err error) {
	name, content, err = d.st.ResolveDeploymentSource(ctx, dep)
	if err != nil {
		return "", "", nil, err
	}
	env = dep.Env
	if dep.CurrentRevision > 0 {
		if rev, rerr := d.st.GetDeploymentRevision(ctx, dep.ID, dep.CurrentRevision); rerr == nil {
			content, env = rev.ComposeContent, rev.Env
		}
	}
	return name, content, env, nil
}

// loadProject parses Compose content the same way the agent does.
func loadProject(ctx context.Context, content string, env map[string]string) (*types.Project, error) {
	return loader.LoadWithContext(ctx, types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{{Filename: "compose.yaml", Content: []byte(content)}},
		Environment: env,
	}, func(o *loader.Options) {
		o.SetProjectName("validate", true)
		o.SkipConsistencyCheck = true
	})
}

// validateScale checks a scale request against the service's definition:
// it must exist, the count must be sane, and a service publishing a fixed
// host port can only run one replica.
func validateScale(project *types.Project, service string, replicas int) error {
	svc, ok := project.Services[service]
	if !ok {
		return newActionError(http.StatusNotFound, "service %q not found in this deployment", service)
	}
	if replicas < 1 || replicas > maxReplicas {
		return newActionError(http.StatusBadRequest, "replicas must be between 1 and %d (use stop to run none)", maxReplicas)
	}
	if replicas > 1 {
		for _, port := range svc.Ports {
			if port.Published != "" && !containsRune(port.Published, '-') {
				return newActionError(http.StatusBadRequest, "service %q publishes fixed host port %s, so it can't run more than one replica — remove the host port or use a port range", service, port.Published)
			}
		}
	}
	return nil
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

func (d *deployer) executeScale(ctx context.Context, dep *store.Deployment, p actionParams, a actor) (*actionOutcome, error) {
	name, content, env, err := d.currentContent(ctx, dep)
	if err != nil {
		return nil, err
	}
	project, err := loadProject(ctx, content, env)
	if err != nil {
		return nil, newActionError(http.StatusBadRequest, "failed to parse compose file: %v", err)
	}
	if err := validateScale(project, p.Service, p.Replicas); err != nil {
		return nil, err
	}
	result, err := d.sendServiceCommand(dep, name, content, env, p.Service, p.Replicas, true)
	if err != nil {
		return nil, err
	}
	summary := describeAction(actionScale, p)
	if result.GetSuccess() {
		scales := map[string]int{}
		for k, v := range dep.Scales {
			scales[k] = v
		}
		scales[p.Service] = p.Replicas
		if err := d.st.UpdateDeploymentScales(ctx, dep.ID, scales); err != nil {
			return nil, err
		}
	} else {
		summary += " failed: " + result.GetErrorMessage()
	}
	d.event(ctx, dep.ID, dep.Phase, summary, a.ID)
	d.audit(ctx, a, "deployment.scale", "deployment", dep.ID, summary, p)
	ok := result.GetSuccess()
	return &actionOutcome{DeploymentID: dep.ID, Status: "completed", Success: &ok, Error: result.GetErrorMessage(), Message: summary}, nil
}

func (d *deployer) executeServiceRedeploy(ctx context.Context, dep *store.Deployment, p actionParams, a actor) (*actionOutcome, error) {
	name, content, env, err := d.currentContent(ctx, dep)
	if err != nil {
		return nil, err
	}
	project, err := loadProject(ctx, content, env)
	if err != nil {
		return nil, newActionError(http.StatusBadRequest, "failed to parse compose file: %v", err)
	}
	if _, ok := project.Services[p.Service]; !ok {
		return nil, newActionError(http.StatusNotFound, "service %q not found in this deployment's compose file", p.Service)
	}
	result, err := d.sendServiceCommand(dep, name, content, env, p.Service, dep.Scales[p.Service], false)
	if err != nil {
		return nil, err
	}
	message := fmt.Sprintf("service %q redeployed", p.Service)
	if !result.GetSuccess() {
		message = fmt.Sprintf("service %q redeploy failed: %s", p.Service, result.GetErrorMessage())
	}
	// The deployment's overall phase is left as-is — only one service
	// was touched, the rest of the stack kept running the whole time.
	d.event(ctx, dep.ID, dep.Phase, message, a.ID)
	d.audit(ctx, a, "deployment.redeploy_service", "deployment", dep.ID, message, p)
	ok := result.GetSuccess()
	return &actionOutcome{DeploymentID: dep.ID, Status: "completed", Success: &ok, Error: result.GetErrorMessage(), Message: message}, nil
}

func (d *deployer) sendServiceCommand(dep *store.Deployment, name, content string, env map[string]string, service string, replicas int, scaleOnly bool) (*agentv1.ContainerOpResult, error) {
	requestID, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_DeployService{
			DeployService: &agentv1.DeployServiceCommand{
				RequestId:    requestID,
				ServerId:     dep.ServerID,
				DeploymentId: dep.ID,
				StackName:    name,
				ComposeYaml:  content,
				Env:          env,
				ServiceName:  service,
				Replicas:     int32(replicas),
				ScaleOnly:    scaleOnly,
			},
		},
	}
	result, err := sendAndAwaitContainerOp(d.dispatcher, d.opWaiter, dep.ServerID, requestID, cmd)
	if err != nil {
		if errors.Is(err, deploy.ErrAgentNotConnected) {
			return nil, newActionError(http.StatusConflict, "server not connected")
		}
		return nil, newActionError(http.StatusGatewayTimeout, "%v", err)
	}
	return result, nil
}

// runRequest executes a queued request that has just been claimed (moved
// to executed) and records its outcome.
func (d *deployer) runRequest(ctx context.Context, req *store.DeploymentRequest, a actor) (*actionOutcome, error) {
	dep, err := d.st.GetDeployment(ctx, req.DeploymentID)
	if err != nil {
		_ = d.st.FinishDeploymentRequest(ctx, req.ID, true, err.Error())
		return nil, err
	}
	var p actionParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			_ = d.st.FinishDeploymentRequest(ctx, req.ID, true, "invalid request params")
			return nil, err
		}
	}
	outcome, err := d.execute(ctx, dep, req.Action, p, a)
	if err != nil {
		_ = d.st.FinishDeploymentRequest(ctx, req.ID, true, err.Error())
		d.event(ctx, dep.ID, "failed", fmt.Sprintf("approved %s failed to run: %v", describeAction(req.Action, p), err), a.ID)
		if dep.CurrentRevision == 0 {
			_ = d.st.UpdateDeploymentPhase(ctx, dep.ID, "failed")
		}
		return nil, err
	}
	_ = d.st.FinishDeploymentRequest(ctx, req.ID, false, outcome.Message)
	return outcome, nil
}

// dispatchDeploy sends a DeployStackCommand to serverID and, if that fails
// (agent not currently connected), records and publishes a FAILED event.
func dispatchDeploy(ctx context.Context, log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus, deploymentID, serverID, stackName, composeYAML string, env map[string]string, scales map[string]int, strategy string, revision int) error {
	replicas := make(map[string]int32, len(scales))
	for k, v := range scales {
		replicas[k] = int32(v)
	}
	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_DeployStack{
			DeployStack: &agentv1.DeployStackCommand{
				DeploymentId: deploymentID,
				StackName:    stackName,
				ComposeYaml:  composeYAML,
				Env:          env,
				Replicas:     replicas,
				Strategy:     strategy,
				Revision:     int32(revision),
			},
		},
	}

	if err := dispatcher.Send(serverID, cmd); err != nil {
		log.Warn("failed to dispatch deploy command", "deployment_id", deploymentID, "server_id", serverID, "error", err)
		_ = st.UpdateDeploymentPhase(ctx, deploymentID, "failed")
		if event, addErr := st.AddDeploymentEvent(ctx, deploymentID, "failed", "server not connected: "+err.Error(), ""); addErr == nil {
			events.Publish(deploymentID, event)
		}
		return err
	}
	return nil
}
