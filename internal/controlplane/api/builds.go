package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/compose"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/gitsource"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

const (
	// buildTimeout bounds one image build on the agent.
	buildTimeout = 30 * time.Minute
	// buildAckTimeout is how long an agent has to acknowledge a build. An
	// agent too old to know BuildImageCommand silently ignores it.
	buildAckTimeout = time.Minute
	// buildPollInterval is how often a waiting rollout rechecks the build
	// and the agent's connection, in case a notification was missed.
	buildPollInterval = 5 * time.Second
)

// buildJob is one image a rollout needs on its deployment's server.
type buildJob struct {
	service   string
	tag       string
	spec      compose.BuildSpec
	repoID    string
	gitRef    string
	gitCommit string
}

// planBuilds works out which images rollout ro needs built (or confirmed
// already built) on dep's server before it can be deployed:
//
//   - services with a build: section are built from the linked repository
//     at ro.gitCommit; ro.content gets those sections replaced by the
//     images' tags (the original is kept in ro.sourceContent);
//   - Compose content that already pins built images (rolling back to, or
//     promoting, an earlier revision) needs those images present on this
//     server, and they're rebuilt from their recorded settings if not.
//
// Tags are derived from the commit and the build settings, so an image the
// agent already has is reused rather than rebuilt: redeploying, changing
// only runtime configuration, or rolling back costs no build.
func (d *deployer) planBuilds(ctx context.Context, dep *store.Deployment, ro *rollout) ([]buildJob, error) {
	var jobs []buildJob
	planned := map[string]bool{}

	if compose.HasBuild(ro.content) {
		file, repo, err := d.buildSource(ctx, dep)
		if err != nil {
			return nil, err
		}
		if ro.gitCommit == "" {
			return nil, newActionError(http.StatusBadRequest, "this Compose file uses build:, but it has no Git commit to build from — sync it from its repository first")
		}
		specs, err := compose.BuildSpecs(ro.content, path.Dir(strings.TrimPrefix(file.GitPath, "/")), ro.env)
		if err != nil {
			return nil, newActionError(http.StatusBadRequest, "%v", err)
		}
		gitRef := ro.gitRef
		if gitRef == "" {
			gitRef = file.GitRef
		}
		tags := make(map[string]string, len(specs))
		for _, spec := range specs {
			tag := compose.BuildTag(repo.ID, ro.gitCommit, spec)
			tags[spec.Service] = tag
			planned[tag] = true
			jobs = append(jobs, buildJob{service: spec.Service, tag: tag, spec: spec, repoID: repo.ID, gitRef: gitRef, gitCommit: ro.gitCommit})
		}
		pinned, err := compose.PinBuiltImages(ro.content, tags)
		if err != nil {
			return nil, newActionError(http.StatusBadRequest, "failed to prepare the Compose file: %v", err)
		}
		ro.sourceContent, ro.content = ro.content, pinned
	}

	images, err := composeImages(ctx, ro.content, ro.env)
	if err != nil {
		// Invalid content fails at the agent with the parser's own error.
		return jobs, nil
	}
	for _, image := range images {
		if !compose.IsBuiltImage(image) || planned[image] {
			continue
		}
		planned[image] = true
		b, err := d.st.LatestBuildForTag(ctx, image)
		if errors.Is(err, store.ErrNotFound) || (err == nil && b.GitRepositoryID == nil) {
			// Nothing to rebuild it from; the server may still have it.
			continue
		}
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, buildJob{
			service: b.Service, tag: image, repoID: *b.GitRepositoryID, gitRef: b.GitRef, gitCommit: b.GitCommit,
			spec: compose.BuildSpec{
				Service: b.Service, Context: b.ContextPath, Dockerfile: b.Dockerfile, Target: b.Target,
				Args: b.BuildArgs, Labels: b.Labels, NoCache: b.NoCache,
			},
		})
	}
	return jobs, nil
}

// buildSource is the Compose file and repository a deployment's builds
// come from.
func (d *deployer) buildSource(ctx context.Context, dep *store.Deployment) (*store.ComposeFile, *store.GitRepository, error) {
	notLinked := newActionError(http.StatusBadRequest, "services with build: can only be deployed from a Compose file linked to a Git repository — the image is built from that repository")
	if dep.ComposeFileID == nil {
		return nil, nil, notLinked
	}
	file, err := d.st.GetComposeFile(ctx, *dep.ComposeFileID)
	if err != nil {
		return nil, nil, err
	}
	if file.GitRepositoryID == nil {
		return nil, nil, notLinked
	}
	repo, _, err := d.gitRepoFor(ctx, file)
	if err != nil {
		return nil, nil, err
	}
	return file, repo, nil
}

// describeChanges summarizes what rollout ro changes compared with the
// revision dep is currently running: the commit, the Compose configuration,
// and the deployment environment. Whether the code in a build context
// changed shows in the build events (an unchanged context reuses its
// image). Empty for a first deploy.
func (d *deployer) describeChanges(ctx context.Context, dep *store.Deployment, ro *rollout) string {
	if dep.CurrentRevision == 0 {
		return ""
	}
	prev, err := d.st.GetDeploymentRevision(ctx, dep.ID, dep.CurrentRevision)
	if err != nil {
		return ""
	}
	var parts []string
	if prev.GitCommit != "" && ro.gitCommit != "" && prev.GitCommit != ro.gitCommit {
		parts = append(parts, fmt.Sprintf("commit %s → %s", shortCommit(prev.GitCommit), shortCommit(ro.gitCommit)))
	}
	source := ro.content
	if ro.sourceContent != "" {
		source = ro.sourceContent
	}
	if changes := compose.DescribeChanges(prev.SourceOf(), source); len(changes) > 0 {
		parts = append(parts, "config: "+strings.Join(changes, "; "))
	}
	if env := compose.DescribeEnvChanges(prev.Env, ro.env); env != "" {
		parts = append(parts, "environment variables: "+env)
	}
	if len(parts) == 0 {
		return "no changes to the commit or configuration"
	}
	return strings.Join(parts, " · ")
}

// supersedeBuilds stops a deployment's builds still running for revisions
// before `revision`: a newer rollout replaces them, and the newest push
// wins.
func (d *deployer) supersedeBuilds(ctx context.Context, depID string, revision int) {
	active, err := d.st.SupersedeActiveBuilds(ctx, depID, revision, fmt.Sprintf("superseded by revision %d", revision))
	if err != nil {
		d.log.Error("failed to supersede builds", "deployment_id", depID, "error", err)
		return
	}
	for _, b := range active {
		_ = d.dispatcher.Send(b.ServerID, &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_StopStream{StopStream: &agentv1.StopStreamCommand{RequestId: b.ID}},
		})
		d.builds.Publish(&agentv1.BuildStatus{BuildId: b.ID})
	}
	if len(active) > 0 {
		d.event(ctx, depID, "building", fmt.Sprintf("stopped %d image build(s) of an older revision — revision %d replaces it", len(active), revision), "")
	}
}

// buildThenDeploy runs a rollout's builds one after another, then deploys
// revision. It gives up quietly if a newer rollout takes over meanwhile
// (that one reports its own outcome), and fails the rollout — leaving the
// previous revision running — if a build fails.
func (d *deployer) buildThenDeploy(dep *store.Deployment, ro *rollout, jobs []buildJob, revision int, a actor) {
	ctx := context.Background()
	superseded := func() {
		_ = d.st.UpdateRevisionStatus(ctx, dep.ID, revision, "superseded", "a newer rollout replaced it before it was deployed")
	}
	for i, job := range jobs {
		if !d.stillCurrent(ctx, dep.ID, revision) {
			superseded()
			return
		}
		status, message := d.runBuild(ctx, dep, revision, job, a, i+1, len(jobs))
		switch status {
		case store.BuildSucceeded:
		case store.BuildSuperseded:
			superseded()
			return
		default:
			d.failRollout(ctx, dep.ID, revision, fmt.Sprintf("image build for service %q %s: %s", job.service, status, message))
			return
		}
	}
	if !d.stillCurrent(ctx, dep.ID, revision) {
		superseded()
		return
	}
	if err := dispatchDeploy(ctx, d.log, d.st, d.dispatcher, d.events, dep.ID, dep.ServerID, ro.name, ro.content, ro.env, ro.scales, dep.UpdateStrategy, revision); err != nil {
		_ = d.st.UpdateRevisionStatus(ctx, dep.ID, revision, "failed", "server not connected")
		_ = d.st.RevertToPreviousRevision(ctx, dep.ID)
	}
}

// runBuild runs one build on dep's server and waits for its final status.
func (d *deployer) runBuild(ctx context.Context, dep *store.Deployment, revision int, job buildJob, a actor, n, total int) (status, message string) {
	repo, err := d.st.GetGitRepository(ctx, job.repoID)
	if err != nil {
		return store.BuildFailed, "its Git repository is no longer configured"
	}
	id, err := d.st.InsertImageBuild(ctx, store.NewImageBuild{
		DeploymentID: dep.ID, Revision: revision, ServerID: dep.ServerID, Service: job.service, ImageTag: job.tag,
		GitRepositoryID: job.repoID, GitRef: job.gitRef, GitCommit: job.gitCommit,
		ContextPath: job.spec.Context, Dockerfile: job.spec.Dockerfile, Target: job.spec.Target,
		BuildArgs: job.spec.Args, Labels: job.spec.Labels, NoCache: job.spec.NoCache, CreatedBy: a.ID,
	})
	if err != nil {
		d.log.Error("failed to record build", "deployment_id", dep.ID, "error", err)
		return store.BuildFailed, "internal error recording the build"
	}

	labels := make(map[string]string, len(job.spec.Labels)+4)
	for k, v := range job.spec.Labels {
		labels[k] = v
	}
	// These labels identify platform builds for rollback and image cleanup;
	// a Compose file cannot replace them.
	for k, v := range map[string]string{
		"pspocketedge.build":      id,
		"pspocketedge.deployment": dep.ID,
		"pspocketedge.service":    job.service,
		"pspocketedge.commit":     job.gitCommit,
	} {
		labels[k] = v
	}
	src := gitsource.Repo{URL: repo.URL, Provider: repo.Provider, Username: repo.Username, Token: repo.Token}
	cmd := &agentv1.BuildImageCommand{
		BuildId: id, DeploymentId: dep.ID, ImageTag: job.tag,
		RepoUrl: repo.URL, GitRef: job.gitRef, GitCommit: job.gitCommit, GitToken: repo.Token,
		ContextPath: job.spec.Context, Dockerfile: job.spec.Dockerfile, Target: job.spec.Target,
		BuildArgs: job.spec.Args, Labels: labels, NoCache: job.spec.NoCache,
		TimeoutSeconds: int32(buildTimeout / time.Second),
		SettingsKey:    compose.SettingsKey(job.spec),
	}
	if repo.Token != "" {
		cmd.GitUsername = src.AuthUsername()
	}

	counter := ""
	if total > 1 {
		counter = fmt.Sprintf(" (%d of %d)", n, total)
	}
	d.event(ctx, dep.ID, "building", fmt.Sprintf("building image for service %q from %s at %s%s", job.service, job.gitRef, shortCommit(job.gitCommit), counter), "")

	state := d.awaitBuild(ctx, dep.ServerID, id, cmd)
	switch {
	case state.Status == store.BuildSucceeded && state.Reused:
		d.event(ctx, dep.ID, "building", fmt.Sprintf("image for service %q is unchanged — reused %s", job.service, job.tag), "")
	case state.Status == store.BuildSucceeded:
		d.event(ctx, dep.ID, "building", fmt.Sprintf("image for service %q built: %s", job.service, job.tag), "")
	case state.Status == store.BuildSuperseded:
		d.log.Info("build superseded", "deployment_id", dep.ID, "build_id", id)
	}
	return state.Status, state.Message
}

// awaitBuild sends cmd to serverID and waits for the build to finish. The
// stored build is the source of truth; agent notifications only prompt a
// recheck, and a periodic recheck covers any that were missed. The build
// fails if the agent doesn't acknowledge it, disconnects, or overruns.
func (d *deployer) awaitBuild(ctx context.Context, serverID, id string, cmd *agentv1.BuildImageCommand) store.BuildState {
	ch, unsubscribe := d.builds.Subscribe(id)
	defer unsubscribe()

	generation, _ := d.dispatcher.Connection(serverID)
	if err := d.dispatcher.Send(serverID, &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_BuildImage{BuildImage: cmd}}); err != nil {
		message := "server not connected: " + err.Error()
		_, _ = d.st.FinishImageBuild(ctx, id, store.BuildFailed, message)
		return store.BuildState{Status: store.BuildFailed, Message: message}
	}

	fail := func(message string) store.BuildState {
		if changed, _ := d.st.FinishImageBuild(ctx, id, store.BuildFailed, message); changed {
			_ = d.dispatcher.Send(serverID, &agentv1.ControlMessage{
				Payload: &agentv1.ControlMessage_StopStream{StopStream: &agentv1.StopStreamCommand{RequestId: id}},
			})
			d.builds.Publish(&agentv1.BuildStatus{BuildId: id})
		}
		if state, err := d.st.GetImageBuildState(ctx, id); err == nil {
			return state
		}
		return store.BuildState{Status: store.BuildFailed, Message: message}
	}

	acked := false
	ackDeadline := time.NewTimer(buildAckTimeout)
	defer ackDeadline.Stop()
	deadline := time.NewTimer(buildTimeout + 2*time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(buildPollInterval)
	defer ticker.Stop()
	for {
		select {
		case bs := <-ch:
			acked = acked || bs.GetPhase() != agentv1.BuildPhase_BUILD_PHASE_UNSPECIFIED
			if bs.GetLog() != "" {
				continue
			}
		case <-ticker.C:
			if gen, connected := d.dispatcher.Connection(serverID); !connected || gen != generation {
				return fail("the server disconnected during the build")
			}
		case <-ackDeadline.C:
			if state, err := d.st.GetImageBuildState(ctx, id); !acked && err == nil && state.Status == store.BuildQueued {
				return fail("the server's agent didn't respond to the build — update the agent to a version that supports building images")
			}
			continue
		case <-deadline.C:
			return fail(fmt.Sprintf("the build didn't finish within %s", buildTimeout))
		}
		state, err := d.st.GetImageBuildState(ctx, id)
		if err != nil {
			d.log.Error("failed to check build", "build_id", id, "error", err)
			continue
		}
		if store.IsFinalBuildStatus(state.Status) {
			return state
		}
	}
}

func (d *deployer) stillCurrent(ctx context.Context, depID string, revision int) bool {
	current, err := d.st.IsCurrentRevision(ctx, depID, revision)
	return err == nil && current
}

// failRollout marks revision failed before it reached the server. If it's
// still the deployment's current revision, the deployment goes back to the
// revision that's actually running (untouched, since nothing was deployed).
func (d *deployer) failRollout(ctx context.Context, depID string, revision int, message string) {
	_ = d.st.UpdateRevisionStatus(ctx, depID, revision, "failed", message)
	if d.stillCurrent(ctx, depID, revision) {
		_ = d.st.UpdateDeploymentPhase(ctx, depID, "failed")
		_ = d.st.RevertToPreviousRevision(ctx, depID)
		if dep, err := d.st.GetDeployment(ctx, depID); err == nil && dep.CurrentRevision != revision {
			message += fmt.Sprintf(" — revision %d is still running", dep.CurrentRevision)
		}
	}
	d.event(ctx, depID, "failed", fmt.Sprintf("revision %d not deployed: %s", revision, message), "")
}

// maxListedBuilds bounds GET /api/deployments/{id}/builds.
const maxListedBuilds = 50

func handleListDeploymentBuilds(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dep, ok := d.loadDeploymentOr404(w, r)
		if !ok {
			return
		}
		builds, err := d.st.ListDeploymentBuilds(r.Context(), dep.ID, maxListedBuilds)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		writeJSON(w, http.StatusOK, builds)
	}
}

func handleGetBuild(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := d.st.GetImageBuild(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "build not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		writeJSON(w, http.StatusOK, b)
	}
}

// handleCancelBuild stops a running build. The rollout waiting on it then
// fails, and the deployment keeps running its previous revision.
func handleCancelBuild(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		b, err := d.st.GetImageBuild(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "build not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		changed, err := d.st.FinishImageBuild(r.Context(), b.ID, store.BuildCancelled, "cancelled by "+a.Email)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if !changed {
			writeActionError(w, d.log, newActionError(http.StatusConflict, "the build already finished"))
			return
		}
		_ = d.dispatcher.Send(b.ServerID, &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_StopStream{StopStream: &agentv1.StopStreamCommand{RequestId: b.ID}},
		})
		d.builds.Publish(&agentv1.BuildStatus{BuildId: b.ID})
		d.audit(r.Context(), a, "build.cancel", "deployment", b.DeploymentID, fmt.Sprintf("cancelled the image build for service %q", b.Service), map[string]any{"buildId": b.ID})
		w.WriteHeader(http.StatusNoContent)
	}
}

// buildStreamMessage is one message on a build's WebSocket stream.
type buildStreamMessage struct {
	// "build" (the full build, output included: the first message, and
	// again once the build is over), "log" (new output), or "status".
	Type    string            `json:"type"`
	Build   *store.ImageBuild `json:"build,omitempty"`
	Seq     int64             `json:"seq,omitempty"`
	Text    string            `json:"text,omitempty"`
	Status  string            `json:"status,omitempty"`
	Message string            `json:"message,omitempty"`
}

// handleBuildStream streams a build's output and status over a WebSocket:
// the build so far, then new output and status changes as they happen,
// then the finished build, after which the server closes the stream. Like
// the deployment stream, it authenticates with a ?token= query parameter
// because browsers can't set headers on a WebSocket handshake.
func handleBuildStream(log *slog.Logger, st *store.Store, authMgr *auth.Manager, builds *deploy.BuildBus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := authMgr.AuthenticateRequest(r, r.URL.Query().Get("token")); err != nil {
			auth.WriteAuthError(w, err)
			return
		}
		id := r.PathValue("id")
		// Subscribe before loading the build so no output lands in between.
		ch, unsubscribe := builds.Subscribe(id)
		defer unsubscribe()
		b, err := st.GetImageBuild(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "build not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load build", "build_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Warn("failed to upgrade build stream", "build_id", id, "error", err)
			return
		}
		defer conn.Close()
		if err := conn.WriteJSON(buildStreamMessage{Type: "build", Build: b}); err != nil {
			return
		}
		if store.IsFinalBuildStatus(b.Status) {
			closeBuildStream(conn)
			return
		}
		lastSeq, lastStatus := b.LogSeq, b.Status

		closed := make(chan struct{})
		go func() {
			for {
				if _, _, err := conn.NextReader(); err != nil {
					close(closed)
					return
				}
			}
		}()

		ticker := time.NewTicker(buildPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-closed:
				return
			case bs := <-ch:
				if bs.GetLog() != "" {
					if bs.GetLogSeq() > lastSeq {
						lastSeq = bs.GetLogSeq()
						if err := conn.WriteJSON(buildStreamMessage{Type: "log", Seq: bs.GetLogSeq(), Text: bs.GetLog()}); err != nil {
							return
						}
					}
					continue
				}
			case <-ticker.C:
			}
			state, err := st.GetImageBuildState(r.Context(), id)
			if err != nil {
				return
			}
			if store.IsFinalBuildStatus(state.Status) {
				if final, err := st.GetImageBuild(r.Context(), id); err == nil {
					_ = conn.WriteJSON(buildStreamMessage{Type: "build", Build: final})
				}
				closeBuildStream(conn)
				return
			}
			if state.Status != lastStatus {
				lastStatus = state.Status
				if err := conn.WriteJSON(buildStreamMessage{Type: "status", Status: state.Status, Message: state.Message}); err != nil {
					return
				}
			}
		}
	}
}

// closeBuildStream ends a build stream normally once the build is over.
func closeBuildStream(conn *websocket.Conn) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "build finished"), time.Now().Add(time.Second))
}
