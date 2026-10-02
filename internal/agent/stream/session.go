// Package stream manages the agent's persistent connection to the control
// plane: enrolling once, then keeping the bidirectional Session stream open
// with reconnect/backoff, periodic heartbeats, and applying any
// DeployStackCommand the control plane sends.
package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	dockerclient "github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/backoff"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/docker"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/enroll"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/health"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/state"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

const (
	// standardHeartbeatInterval/standardContainerListEvery are unchanged
	// from before edge-device tuning — the common case (cloud VM, dev
	// machine) sees no behavior change.
	standardHeartbeatInterval     = 20 * time.Second
	standardContainerListEvery    = 1
	constrainedHeartbeatInterval  = 60 * time.Second
	constrainedContainerListEvery = 3
	// constrainedMemThreshold buckets a Pi Zero 2 W (512MB) and Pi 3 (1GB)
	// as "constrained" while leaving any 2GB+ cloud VM or dev machine on
	// the standard cadence.
	constrainedMemThreshold = 1536 * 1024 * 1024 // 1.5 GiB

	// bufferMaxEntries bounds the offline heartbeat buffer — comfortably
	// covers an overnight outage at the constrained tier's ~60s cadence
	// without unbounded growth.
	bufferMaxEntries = 500

	minBackoff = 1 * time.Second
	maxBackoff = 60 * time.Second
)

// Runner owns the connection lifecycle: dial, enroll (once, persisting the
// resulting identity to StatePath so a restart doesn't re-enroll with an
// already-consumed token), then keep the Session stream alive, reconnecting
// with backoff on failure.
type Runner struct {
	Addr         string
	Token        string
	Hostname     string
	OS           string
	Arch         string
	AgentVersion string
	StatePath    string
	TLS          bool
	TLSCAFile    string
	// AllowBuilds lets the control plane build images from Git on this
	// server's Docker daemon. Off by default: a Dockerfile's RUN steps
	// execute on this host with the daemon's privileges.
	AllowBuilds bool
	// BuildDir is where builds clone repositories; empty uses a directory
	// under the system temp dir.
	BuildDir     string
	backupClient *http.Client

	log *slog.Logger

	// heartbeatInterval/containerListEvery are set once in Run, from the
	// device's detected tier (see detectTier) — every session and the
	// disconnected-buffering loop both read them.
	heartbeatInterval  time.Duration
	containerListEvery int
	// bufferPath is where the offline heartbeat buffer lives, derived from
	// StatePath; empty (buffering disabled) if StatePath is unset.
	bufferPath string

	// deploymentLocks holds one *sync.Mutex per deployment ID, so every
	// operation that changes a deployment's containers (deploy/redeploy,
	// restore, service redeploy/scale, removal) runs one at a time for that
	// deployment — two overlapping rollouts would otherwise fight over the
	// same deterministic container names.
	deploymentLocks sync.Map

	// buildSlot admits one image build at a time — builds are CPU-,
	// memory- and disk-heavy, and a small edge device can't run several.
	buildSlot chan struct{}
}

// lockDeployment serializes work on one deployment; call the returned
// function to release it.
func (r *Runner) lockDeployment(deploymentID string) func() {
	m, _ := r.deploymentLocks.LoadOrStore(deploymentID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func New(log *slog.Logger, addr, token, hostname, osName, arch, agentVersion, statePath string) *Runner {
	return &Runner{
		Addr:         addr,
		Token:        token,
		Hostname:     hostname,
		OS:           osName,
		Arch:         arch,
		AgentVersion: agentVersion,
		StatePath:    statePath,
		log:          log,
		buildSlot:    make(chan struct{}, 1),
	}
}

// detectTier samples total host memory once and classifies this device as
// "standard" or "constrained" — the one hardware signal simple enough to
// separate a Pi from a VM without a whole capability-negotiation scheme. A
// collection failure (unusual/sandboxed environment) falls back to the
// standard tier rather than guessing constrained, since that's the safe
// (unthrottled) default.
func (r *Runner) detectTier(ctx context.Context) {
	total, err := health.TotalMemory(ctx)
	if err != nil {
		r.log.Warn("failed to detect total memory, assuming standard tier", "error", err)
		r.heartbeatInterval = standardHeartbeatInterval
		r.containerListEvery = standardContainerListEvery
		return
	}

	if total < constrainedMemThreshold {
		r.heartbeatInterval = constrainedHeartbeatInterval
		r.containerListEvery = constrainedContainerListEvery
		r.log.Info("device tier detected", "tier", "constrained", "total_memory_bytes", total)
	} else {
		r.heartbeatInterval = standardHeartbeatInterval
		r.containerListEvery = standardContainerListEvery
		r.log.Info("device tier detected", "tier", "standard", "total_memory_bytes", total)
	}
}

// Run blocks, enrolling once (or reusing a persisted identity) and then
// looping the Session stream with reconnect/backoff until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	transport, err := transportCredentials(r.Addr, r.TLS, r.TLSCAFile)
	if err != nil {
		return err
	}
	r.backupClient, err = backupHTTPClient(r.TLSCAFile)
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(r.Addr, grpc.WithTransportCredentials(transport))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := agentv1.NewAgentSessionClient(conn)

	identity, err := r.loadOrEnroll(ctx, client)
	if err != nil {
		return err
	}
	r.log.Info("agent identity ready", "server_id", identity.ServerID)

	// Constructing the Docker client doesn't require a live daemon — it
	// only fails later, when a deploy is actually attempted. So a host
	// without Docker reachable yet still enrolls and heartbeats fine;
	// deploy commands just report FAILED until it's reachable.
	dockerCli, err := docker.NewClient()
	if err != nil {
		r.log.Warn("failed to create docker client, deploy commands will fail until this is resolved", "error", err)
	}

	r.detectTier(ctx)
	if r.StatePath != "" {
		r.bufferPath = filepath.Join(filepath.Dir(r.StatePath), "heartbeat-buffer.jsonl")
	}

	backoffDuration := minBackoff
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := r.runSession(ctx, client, identity, dockerCli); err != nil {
			r.log.Warn("session ended, reconnecting", "error", err, "backoff", backoffDuration)
		}

		// While waiting out the reconnect backoff, keep sampling and
		// buffering host resources — so a long outage (many failed
		// reconnect attempts, each capped at maxBackoff) still yields
		// history once reconnected, instead of a silent gap.
		waitCtx, cancelWait := context.WithTimeout(ctx, backoff.Jitter(backoffDuration))
		r.bufferWhileDisconnected(waitCtx)
		cancelWait()
		if ctx.Err() != nil {
			return ctx.Err()
		}

		backoffDuration *= 2
		if backoffDuration > maxBackoff {
			backoffDuration = maxBackoff
		}
	}
}

// bufferWhileDisconnected samples host resources on the current tier's
// interval and appends them to the offline heartbeat buffer, until ctx is
// done (either the reconnect wait elapsed or the agent is shutting down).
// A no-op wait (buffering disabled) when bufferPath is unset — local dev
// runs without persistent state don't need this.
func (r *Runner) bufferWhileDisconnected(ctx context.Context) {
	if r.bufferPath == "" {
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap, err := health.Collect(ctx)
			if err != nil {
				r.log.Warn("failed to collect host metrics while disconnected", "error", err)
				continue
			}
			buf := health.Open(r.bufferPath, bufferMaxEntries)
			if err := buf.Append(health.Sample{
				RecordedAt:  time.Now(),
				CPUPercent:  snap.CPUPercent,
				MemPercent:  snap.MemPercent,
				DiskPercent: snap.DiskPercent,
			}); err != nil {
				r.log.Warn("failed to append to heartbeat buffer", "error", err)
			}
		}
	}
}

func (r *Runner) loadOrEnroll(ctx context.Context, client agentv1.AgentSessionClient) (*state.Identity, error) {
	if r.StatePath != "" {
		if existing, err := state.Load(r.StatePath); err != nil {
			r.log.Warn("failed to read agent state file, will re-enroll", "path", r.StatePath, "error", err)
		} else if existing != nil {
			return existing, nil
		}
	}

	identity, err := enroll.Enroll(ctx, client, r.Token, r.Hostname, r.OS, r.Arch, r.AgentVersion)
	if err != nil {
		return nil, err
	}
	r.log.Info("enrolled with control plane", "server_id", identity.ServerID)

	if r.StatePath != "" {
		if err := state.Save(r.StatePath, identity); err != nil {
			r.log.Warn("failed to persist agent state, will re-enroll on restart", "path", r.StatePath, "error", err)
		}
	}

	return identity, nil
}

func (r *Runner) runSession(ctx context.Context, client agentv1.AgentSessionClient, identity *state.Identity, dockerCli *dockerclient.Client) error {
	authedCtx := metadata.AppendToOutgoingContext(ctx,
		"authorization", "Bearer "+identity.Credential,
		"x-server-id", identity.ServerID,
	)

	stream, err := client.Session(authedCtx)
	if err != nil {
		return err
	}

	// Deploys in flight when this session ends must not keep running
	// against a Docker daemon indefinitely while silently unable to report
	// status anywhere — sessionCtx is cancelled the moment runSession
	// returns (by any path), which both aborts the Deploy() call's Docker
	// SDK operations and unblocks report()'s ctx.Done() case below, so
	// that goroutine exits instead of leaking forever on a channel send
	// nobody's reading from.
	sessionCtx, cancelSession := context.WithCancel(ctx)
	defer cancelSession()

	// stream.Send is not safe for concurrent use, but heartbeats (from this
	// function's own loop) and deploy-status reports (from handleDeploy,
	// running in its own goroutine per in-flight deploy) both need to write
	// to it — so every writer funnels through this channel, and only the
	// select loop below ever calls stream.Send directly.
	outbound := make(chan *agentv1.AgentMessage, 16)

	// streams tracks in-progress log tails and exec sessions by
	// request_id, so a later message for the same request_id (a
	// StopStreamCommand, or an ExecInputCommand carrying keystrokes) can be
	// routed to that already-running goroutine instead of the switch below
	// spawning a new one — unlike every other command, these two are
	// multi-message conversations, not one-shot request/reply.
	streams := newActiveStreams()

	// inventoryChanged asks the loop below for an immediate heartbeat with
	// the container list, after a command that created, removed, renamed
	// or restarted containers — so the control plane acts on current
	// container IDs instead of waiting up to a heartbeat interval for them.
	// Buffered by one: a burst of changes collapses into one report.
	inventoryChanged := make(chan struct{}, 1)
	changesContainers := func(handle func()) {
		go func() {
			handle()
			select {
			case inventoryChanged <- struct{}{}:
			default:
			}
		}()
	}

	recvErrCh := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				recvErrCh <- err
				return
			}
			switch {
			case msg.GetStopStream() != nil:
				streams.stop(msg.GetStopStream().GetRequestId())
			case msg.GetExecInput() != nil:
				streams.sendInput(msg.GetExecInput())
			case msg.GetDeployStack() != nil:
				changesContainers(func() { r.handleDeploy(sessionCtx, dockerCli, msg.GetDeployStack(), outbound) })
			case msg.GetBackup() != nil:
				go r.handleBackup(sessionCtx, dockerCli, msg.GetBackup(), identity.Credential, outbound)
			case msg.GetRestore() != nil:
				changesContainers(func() { r.handleRestore(sessionCtx, dockerCli, msg.GetRestore(), identity.Credential, outbound) })
			case msg.GetInspectContainer() != nil:
				go r.handleInspectContainer(sessionCtx, dockerCli, msg.GetInspectContainer(), outbound)
			case msg.GetContainerAction() != nil:
				changesContainers(func() { r.handleContainerAction(sessionCtx, dockerCli, msg.GetContainerAction(), outbound) })
			case msg.GetCreateContainer() != nil:
				changesContainers(func() { r.handleCreateContainer(sessionCtx, dockerCli, msg.GetCreateContainer(), outbound) })
			case msg.GetRenameContainer() != nil:
				changesContainers(func() { r.handleRenameContainer(sessionCtx, dockerCli, msg.GetRenameContainer(), outbound) })
			case msg.GetCloneContainer() != nil:
				changesContainers(func() { r.handleCloneContainer(sessionCtx, dockerCli, msg.GetCloneContainer(), outbound) })
			case msg.GetRecreateContainer() != nil:
				changesContainers(func() { r.handleRecreateContainer(sessionCtx, dockerCli, msg.GetRecreateContainer(), outbound) })
			case msg.GetUpdateRestartPolicy() != nil:
				go r.handleUpdateRestartPolicy(sessionCtx, dockerCli, msg.GetUpdateRestartPolicy(), outbound)
			case msg.GetUpdateResourceLimits() != nil:
				go r.handleUpdateResourceLimits(sessionCtx, dockerCli, msg.GetUpdateResourceLimits(), outbound)
			case msg.GetUndeploy() != nil:
				changesContainers(func() { r.handleUndeploy(sessionCtx, dockerCli, msg.GetUndeploy(), outbound) })
			case msg.GetDeployService() != nil:
				changesContainers(func() { r.handleDeployService(sessionCtx, dockerCli, msg.GetDeployService(), outbound) })
			case msg.GetBuildImage() != nil:
				go r.handleBuildImage(sessionCtx, dockerCli, msg.GetBuildImage(), outbound, streams)
			case msg.GetListImages() != nil:
				go r.handleListImages(sessionCtx, dockerCli, msg.GetListImages(), outbound)
			case msg.GetInspectImage() != nil:
				go r.handleInspectImage(sessionCtx, dockerCli, msg.GetInspectImage(), outbound)
			case msg.GetPullImage() != nil:
				go r.handlePullImage(sessionCtx, dockerCli, msg.GetPullImage(), outbound)
			case msg.GetPushImage() != nil:
				go r.handlePushImage(sessionCtx, dockerCli, msg.GetPushImage(), outbound)
			case msg.GetRemoveImage() != nil:
				go r.handleRemoveImage(sessionCtx, dockerCli, msg.GetRemoveImage(), outbound)
			case msg.GetPruneImages() != nil:
				go r.handlePruneImages(sessionCtx, dockerCli, msg.GetPruneImages(), outbound)
			case msg.GetStreamLogs() != nil:
				go r.handleStreamLogs(sessionCtx, dockerCli, msg.GetStreamLogs(), outbound, streams)
			case msg.GetListEvents() != nil:
				go r.handleListEvents(sessionCtx, dockerCli, msg.GetListEvents(), outbound)
			case msg.GetExecStart() != nil:
				go r.handleExecStart(sessionCtx, dockerCli, msg.GetExecStart(), outbound, streams)
			case msg.GetListNetworks() != nil:
				go r.handleListNetworks(sessionCtx, dockerCli, msg.GetListNetworks(), outbound)
			case msg.GetCreateNetwork() != nil:
				go r.handleCreateNetwork(sessionCtx, dockerCli, msg.GetCreateNetwork(), outbound)
			case msg.GetRemoveNetwork() != nil:
				go r.handleRemoveNetwork(sessionCtx, dockerCli, msg.GetRemoveNetwork(), outbound)
			case msg.GetConnectContainerToNetwork() != nil:
				changesContainers(func() {
					r.handleConnectContainerToNetwork(sessionCtx, dockerCli, msg.GetConnectContainerToNetwork(), outbound)
				})
			case msg.GetDisconnectContainerFromNetwork() != nil:
				changesContainers(func() {
					r.handleDisconnectContainerFromNetwork(sessionCtx, dockerCli, msg.GetDisconnectContainerFromNetwork(), outbound)
				})
			case msg.GetListVolumes() != nil:
				go r.handleListVolumes(sessionCtx, dockerCli, msg.GetListVolumes(), outbound)
			case msg.GetCreateVolume() != nil:
				go r.handleCreateVolume(sessionCtx, dockerCli, msg.GetCreateVolume(), outbound)
			case msg.GetRemoveVolume() != nil:
				go r.handleRemoveVolume(sessionCtx, dockerCli, msg.GetRemoveVolume(), outbound)
			case msg.GetInspectVolume() != nil:
				go r.handleInspectVolume(sessionCtx, dockerCli, msg.GetInspectVolume(), outbound)
			case msg.GetVolumeFile() != nil:
				go r.handleVolumeFile(sessionCtx, dockerCli, msg.GetVolumeFile(), outbound)
			}
		}
	}()

	r.flushBufferedHeartbeats(ctx, stream, identity.ServerID)

	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()

	// tick counts heartbeats sent on this connection, starting fresh on
	// every reconnect — tick 0 always refreshes the container list, so a
	// just-(re)connected agent shows current state immediately rather than
	// waiting up to containerListEvery ticks for it.
	tick := 0
	sendHeartbeat := func() error {
		msg := r.heartbeatMessage(ctx, dockerCli, identity.ServerID, tick)
		tick++
		return stream.Send(msg)
	}
	// sendInventory is an out-of-schedule heartbeat that always carries
	// the container list (tick 0 refreshes it) and leaves the regular
	// tick count alone.
	sendInventory := func() error {
		return stream.Send(r.heartbeatMessage(ctx, dockerCli, identity.ServerID, 0))
	}

	// Send an immediate heartbeat on connect rather than waiting a full
	// interval, so a fresh/reconnected agent shows up promptly.
	if err := sendHeartbeat(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErrCh:
			return err
		case msg := <-outbound:
			if err := stream.Send(msg); err != nil {
				return err
			}
		case <-inventoryChanged:
			if err := sendInventory(); err != nil {
				return err
			}
		case <-ticker.C:
			if err := sendHeartbeat(); err != nil {
				return err
			}
		}
	}
}

// flushBufferedHeartbeats drains any samples accumulated while disconnected
// (see bufferWhileDisconnected) and sends each as a normal Heartbeat
// message carrying its original sample time — so a chart built on this
// data shows what actually happened during the outage, not a flat gap.
// Best-effort: a send failure here just means those samples are lost (the
// buffer is already cleared by DrainAll), not that the session aborts —
// the live heartbeat loop below is what matters for reconnecting at all.
func (r *Runner) flushBufferedHeartbeats(ctx context.Context, stream agentv1.AgentSession_SessionClient, serverID string) {
	if r.bufferPath == "" {
		return
	}

	buf := health.Open(r.bufferPath, bufferMaxEntries)
	samples, err := buf.DrainAll()
	if err != nil {
		r.log.Warn("failed to drain heartbeat buffer", "error", err)
		return
	}
	if len(samples) == 0 {
		return
	}

	r.log.Info("flushing buffered heartbeats", "count", len(samples))
	for _, s := range samples {
		msg := buildHeartbeat(serverID, s.RecordedAt, health.Snapshot{
			CPUPercent:  s.CPUPercent,
			MemPercent:  s.MemPercent,
			DiskPercent: s.DiskPercent,
		}, nil, false, nil, false)
		if err := stream.Send(msg); err != nil {
			r.log.Warn("failed to send buffered heartbeat, remaining samples dropped", "error", err)
			return
		}
	}
}

// handleDeploy applies cmd via the Docker Engine SDK, relaying phase
// transitions back to the control plane over outbound. Runs in its own
// goroutine so a slow image pull doesn't block heartbeats or receiving
// further commands.
func (r *Runner) handleDeploy(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.DeployStackCommand, outbound chan<- *agentv1.AgentMessage) {
	defer r.lockDeployment(cmd.GetDeploymentId())()
	_ = r.deployLocked(ctx, dockerCli, cmd, outbound)
}

// deployLocked is handleDeploy's body, for callers already holding the
// deployment's lock (handleRestore).
func (r *Runner) deployLocked(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.DeployStackCommand, outbound chan<- *agentv1.AgentMessage) error {
	report := func(phase agentv1.DeployPhase, service, message string) {
		select {
		case outbound <- deployStatusMessage(cmd.GetDeploymentId(), cmd.GetRevision(), phase, service, message):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		r.log.Error("cannot deploy, no docker client available", "deployment_id", cmd.GetDeploymentId())
		report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "", "docker client unavailable on this agent")
		return fmt.Errorf("docker client unavailable")
	}

	r.log.Info("deploying stack", "deployment_id", cmd.GetDeploymentId(), "stack", cmd.GetStackName())
	if err := docker.Deploy(ctx, dockerCli, cmd, report); err != nil {
		r.log.Error("deploy failed", "deployment_id", cmd.GetDeploymentId(), "error", err)
		return err
	}
	r.log.Info("deploy succeeded", "deployment_id", cmd.GetDeploymentId())
	return nil
}

// handleBackup snapshots the deployment's volumes and PUTs the resulting
// tar to cmd.UploadUrl, a separate authenticated HTTP endpoint rather than
// the AgentSession stream itself — a multi-GB backup would otherwise share
// the same outbound channel as heartbeats and other commands (see
// runSession's doc comment on why that channel exists), and could starve
// them for as long as the transfer takes.
func (r *Runner) handleBackup(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.BackupCommand, credential string, outbound chan<- *agentv1.AgentMessage) {
	report := func(phase agentv1.TaskPhase, message string) {
		select {
		case outbound <- backupStatusMessage(cmd.GetBackupId(), phase, message):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "docker client unavailable on this agent")
		return
	}

	if cmd.GetQuiesce() {
		// Hold the deployment lock for the whole stop/copy/start window
		// so a redeploy can't start containers against volumes that are
		// mid-copy, or be undone by the restart below.
		defer r.lockDeployment(cmd.GetDeploymentId())()
		report(agentv1.TaskPhase_TASK_PHASE_RUNNING, "stopping containers for a consistent snapshot")
		resume, err := docker.QuiesceDeployment(ctx, dockerCli, cmd.GetDeploymentId())
		// Restart on every path out of here, including a failed stop or
		// upload; use a fresh context so a cancelled session still brings
		// the database back.
		defer func() {
			restartCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if rerr := resume(restartCtx); rerr != nil {
				r.log.Error("failed to restart containers after backup", "backup_id", cmd.GetBackupId(), "error", rerr)
			}
		}()
		if err != nil {
			r.log.Error("backup quiesce failed", "backup_id", cmd.GetBackupId(), "error", err)
			report(agentv1.TaskPhase_TASK_PHASE_FAILED, err.Error())
			return
		}
	}

	var reader io.ReadCloser
	var err error
	if cmd.GetPostgresLogical() {
		report(agentv1.TaskPhase_TASK_PHASE_RUNNING, "exporting PostgreSQL database")
		reader, err = docker.DumpPostgres(ctx, dockerCli, cmd.GetDeploymentId(), cmd.GetPostgresUsername(), cmd.GetPostgresDatabase())
	} else {
		report(agentv1.TaskPhase_TASK_PHASE_RUNNING, "snapshotting volumes")
		reader, err = docker.BackupVolumes(ctx, dockerCli, cmd.GetDeploymentId())
	}
	if err != nil {
		r.log.Error("backup snapshot failed", "backup_id", cmd.GetBackupId(), "error", err)
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "failed to snapshot volumes: "+err.Error())
		return
	}
	defer reader.Close()

	if err := uploadBlob(ctx, r.backupClient, cmd.GetUploadUrl(), credential, reader); err != nil {
		r.log.Error("backup upload failed", "backup_id", cmd.GetBackupId(), "error", err)
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "failed to upload backup: "+err.Error())
		return
	}

	// COMPLETED is recorded by the control plane's blob-upload handler
	// once the bytes actually land (it knows the real size; this stream
	// message doesn't carry the upload's outcome), not reported here.
	r.log.Info("backup uploaded", "backup_id", cmd.GetBackupId())
}

// handleRestore downloads cmd's backup blob, extracts it into the
// deployment's volumes, then redeploys on top of the restored data by
// reusing the normal deploy pipeline — see docker.RestoreVolumes' doc
// comment for why redeploy (not handleRestore itself) owns bringing the
// containers back up.
func (r *Runner) handleRestore(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.RestoreCommand, credential string, outbound chan<- *agentv1.AgentMessage) {
	defer r.lockDeployment(cmd.GetDeploymentId())()
	report := func(phase agentv1.TaskPhase, message string) {
		select {
		case outbound <- restoreStatusMessage(cmd.GetBackupId(), cmd.GetDeploymentId(), phase, message):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "docker client unavailable on this agent")
		return
	}

	report(agentv1.TaskPhase_TASK_PHASE_RUNNING, "downloading and extracting backup")

	body, err := downloadBlob(ctx, r.backupClient, cmd.GetDownloadUrl(), credential)
	if err != nil {
		r.log.Error("restore download failed", "backup_id", cmd.GetBackupId(), "error", err)
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "failed to download backup: "+err.Error())
		return
	}
	defer body.Close()
	if cmd.GetPostgresLogical() {
		if err := docker.RestorePostgresLogical(ctx, dockerCli, cmd.GetDeploymentId(), cmd.GetPostgresUsername(), cmd.GetPostgresDatabase(), body); err != nil {
			r.log.Error("PostgreSQL logical restore failed", "backup_id", cmd.GetBackupId(), "error", err)
			report(agentv1.TaskPhase_TASK_PHASE_FAILED, "PostgreSQL migration failed: "+err.Error())
			return
		}
		report(agentv1.TaskPhase_TASK_PHASE_COMPLETED, "PostgreSQL logical migration completed")
		return
	}

	if err := docker.RestoreVolumes(ctx, dockerCli, cmd, body); err != nil {
		r.log.Error("restore extraction failed", "backup_id", cmd.GetBackupId(), "error", err)
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "failed to restore volumes: "+err.Error())
		return
	}
	r.log.Info("restored volumes, redeploying", "backup_id", cmd.GetBackupId(), "deployment_id", cmd.GetDeploymentId())
	err = r.deployLocked(ctx, dockerCli, &agentv1.DeployStackCommand{
		DeploymentId: cmd.GetDeploymentId(),
		StackName:    cmd.GetStackName(),
		ComposeYaml:  cmd.GetComposeYaml(),
		Env:          cmd.GetEnv(),
	}, outbound)
	if err != nil {
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "redeploy after restore failed: "+err.Error())
		return
	}
	if cmd.GetSyncPostgresPassword() {
		if err := docker.SyncPostgresPassword(ctx, dockerCli, cmd.GetDeploymentId(), cmd.GetPostgresUsername(), cmd.GetPostgresDatabase(), cmd.GetEnv()["DB_PASSWORD"]); err != nil {
			r.log.Error("failed to synchronize target PostgreSQL password after refresh", "deployment_id", cmd.GetDeploymentId(), "error", err)
			report(agentv1.TaskPhase_TASK_PHASE_FAILED, "database restored but target password could not be synchronized: "+err.Error())
			return
		}
	}
	report(agentv1.TaskPhase_TASK_PHASE_COMPLETED, "")
}

// handleInspectContainer runs a live ContainerInspect for cmd.ContainerId
// and replies with a ContainerDetail carrying request_id unchanged, so the
// control plane can match this reply back to the HTTP request that
// triggered it. Runs in its own goroutine, same as handleDeploy/handleBackup
// /handleRestore, so a slow inspect doesn't block heartbeats.
func (r *Runner) handleInspectContainer(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.InspectContainerCommand, outbound chan<- *agentv1.AgentMessage) {
	reply := func(d docker.ContainerDetail) {
		select {
		case outbound <- containerDetailMessage(cmd.GetRequestId(), cmd.GetContainerId(), d):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		reply(docker.ContainerDetail{Found: false, ErrorMessage: "docker client unavailable on this agent"})
		return
	}

	reply(docker.InspectContainer(ctx, dockerCli, cmd.GetContainerId()))
}

// handleContainerAction, handleCreateContainer, handleRenameContainer,
// handleCloneContainer, handleRecreateContainer, and
// handleUpdateRestartPolicy all follow the same shape as
// handleInspectContainer above: run the matching docker/ops.go call in this
// goroutine (so a slow one doesn't block heartbeats), then reply with a
// ContainerOpResult carrying the command's request_id.

func (r *Runner) handleContainerAction(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.ContainerActionCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetContainerId(), errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.ContainerAction(ctx, dockerCli, cmd.GetContainerId(), cmd.GetAction(), cmd.GetTimeoutSeconds(), cmd.GetForce())
	r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetContainerId(), err)
}

func (r *Runner) handleCreateContainer(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.CreateContainerCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyOp(ctx, outbound, cmd.GetRequestId(), "", errors.New("docker client unavailable on this agent"))
		return
	}
	id, err := docker.CreateContainer(ctx, dockerCli, containerConfigFromProto(cmd.GetConfig()))
	r.replyOp(ctx, outbound, cmd.GetRequestId(), id, err)
}

func (r *Runner) handleRenameContainer(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.RenameContainerCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetContainerId(), errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.RenameContainer(ctx, dockerCli, cmd.GetContainerId(), cmd.GetNewName())
	r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetContainerId(), err)
}

func (r *Runner) handleCloneContainer(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.CloneContainerCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyOp(ctx, outbound, cmd.GetRequestId(), "", errors.New("docker client unavailable on this agent"))
		return
	}
	id, err := docker.CloneContainer(ctx, dockerCli, cmd.GetContainerId(), cmd.GetNewName())
	r.replyOp(ctx, outbound, cmd.GetRequestId(), id, err)
}

func (r *Runner) handleRecreateContainer(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.RecreateContainerCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyOp(ctx, outbound, cmd.GetRequestId(), "", errors.New("docker client unavailable on this agent"))
		return
	}
	id, err := docker.RecreateContainer(ctx, dockerCli, cmd.GetContainerId(), containerConfigFromProto(cmd.GetConfig()))
	r.replyOp(ctx, outbound, cmd.GetRequestId(), id, err)
}

func (r *Runner) handleUpdateRestartPolicy(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.UpdateRestartPolicyCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetContainerId(), errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.UpdateRestartPolicy(ctx, dockerCli, cmd.GetContainerId(), cmd.GetRestartPolicyName(), cmd.GetRestartPolicyMaxRetryCount())
	r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetContainerId(), err)
}

func (r *Runner) handleUpdateResourceLimits(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.UpdateResourceLimitsCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetContainerId(), errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.UpdateResourceLimits(ctx, dockerCli, cmd.GetContainerId(), cmd.GetNanoCpus(), cmd.GetMemoryLimitBytes(), cmd.GetMemoryReservationBytes(), cmd.GetPidsLimit())
	r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetContainerId(), err)
}

// handleUndeploy tears down a whole deployment (stack-level remove) — see
// docker.Undeploy. Answered with a ContainerOpResult like every other
// op-style command above, correlated by request_id.
func (r *Runner) handleUndeploy(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.UndeployCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetDeploymentId(), errors.New("docker client unavailable on this agent"))
		return
	}
	defer r.lockDeployment(cmd.GetDeploymentId())()
	err := docker.Undeploy(ctx, dockerCli, cmd.GetDeploymentId())
	r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetDeploymentId(), err)
}

// handleDeployService redeploys one service within an already-deployed
// stack — see docker.DeployService. Answered with a ContainerOpResult,
// correlated by request_id.
func (r *Runner) handleDeployService(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.DeployServiceCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetServiceName(), errors.New("docker client unavailable on this agent"))
		return
	}
	defer r.lockDeployment(cmd.GetDeploymentId())()
	err := docker.DeployService(ctx, dockerCli, cmd.GetDeploymentId(), cmd.GetStackName(), cmd.GetComposeYaml(), cmd.GetEnv(), cmd.GetServiceName(), int(cmd.GetReplicas()), cmd.GetScaleOnly())
	r.replyOp(ctx, outbound, cmd.GetRequestId(), cmd.GetServiceName(), err)
}

// replyOp sends a ContainerOpResult for requestID onto outbound,
// success iff err is nil.
func (r *Runner) replyOp(ctx context.Context, outbound chan<- *agentv1.AgentMessage, requestID, containerID string, err error) {
	select {
	case outbound <- containerOpResultMessage(requestID, containerID, err):
	case <-ctx.Done():
	}
}

// handleListImages, handleInspectImage, handlePullImage, handleRemoveImage,
// and handlePruneImages follow the same shape as handleInspectContainer/
// handleContainerAction above: run the matching docker/images.go call in
// this goroutine (so a slow pull doesn't block heartbeats), then reply with
// the result carrying the command's request_id.

func (r *Runner) handleListImages(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.ListImagesCommand, outbound chan<- *agentv1.AgentMessage) {
	reply := func(images []docker.ImageSummary) {
		select {
		case outbound <- imageListResultMessage(cmd.GetRequestId(), images):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		reply(nil)
		return
	}
	images, err := docker.ListImages(ctx, dockerCli)
	if err != nil {
		r.log.Warn("failed to list images", "error", err)
	}
	reply(images)
}

func (r *Runner) handleInspectImage(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.InspectImageCommand, outbound chan<- *agentv1.AgentMessage) {
	reply := func(d docker.ImageDetail) {
		select {
		case outbound <- imageDetailMessage(cmd.GetRequestId(), d):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		reply(docker.ImageDetail{Found: false, ErrorMessage: "docker client unavailable on this agent"})
		return
	}
	reply(docker.InspectImage(ctx, dockerCli, cmd.GetImageId()))
}

func (r *Runner) handlePullImage(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.PullImageCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyImageOp(ctx, outbound, cmd.GetRequestId(), "", 0, errors.New("docker client unavailable on this agent"))
		return
	}
	var auth *docker.RegistryAuth
	if a := cmd.GetAuth(); a != nil && (a.GetUsername() != "" || a.GetPassword() != "") {
		auth = &docker.RegistryAuth{Username: a.GetUsername(), Password: a.GetPassword()}
	}
	err := docker.PullImage(ctx, dockerCli, cmd.GetImageRef(), auth)
	r.replyImageOp(ctx, outbound, cmd.GetRequestId(), cmd.GetImageRef(), 0, err)
}

func (r *Runner) handlePushImage(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.PushImageCommand, outbound chan<- *agentv1.AgentMessage) {
	if !r.AllowBuilds {
		r.replyImageOp(ctx, outbound, cmd.GetRequestId(), "", 0, errors.New("image builds and pushes are disabled on this server"))
		return
	}
	if dockerCli == nil {
		r.replyImageOp(ctx, outbound, cmd.GetRequestId(), "", 0, errors.New("docker client unavailable on this agent"))
		return
	}
	var auth *docker.RegistryAuth
	if a := cmd.GetAuth(); a != nil && (a.GetUsername() != "" || a.GetPassword() != "") {
		auth = &docker.RegistryAuth{Username: a.GetUsername(), Password: a.GetPassword()}
	}
	pushCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	err := docker.PushImage(pushCtx, dockerCli, cmd.GetSourceTag(), cmd.GetTargetRef(), auth)
	r.replyImageOp(ctx, outbound, cmd.GetRequestId(), cmd.GetTargetRef(), 0, err)
}

func (r *Runner) handleRemoveImage(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.RemoveImageCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyImageOp(ctx, outbound, cmd.GetRequestId(), cmd.GetImageId(), 0, errors.New("docker client unavailable on this agent"))
		return
	}
	err := docker.RemoveImage(ctx, dockerCli, cmd.GetImageId(), cmd.GetForce())
	r.replyImageOp(ctx, outbound, cmd.GetRequestId(), cmd.GetImageId(), 0, err)
}

func (r *Runner) handlePruneImages(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.PruneImagesCommand, outbound chan<- *agentv1.AgentMessage) {
	if dockerCli == nil {
		r.replyImageOp(ctx, outbound, cmd.GetRequestId(), "", 0, errors.New("docker client unavailable on this agent"))
		return
	}
	reclaimed, err := docker.PruneImages(ctx, dockerCli, cmd.GetAll())
	r.replyImageOp(ctx, outbound, cmd.GetRequestId(), "", reclaimed, err)
}

// replyImageOp sends an ImageOpResult for requestID onto outbound, success
// iff err is nil — the image-op counterpart to replyOp above.
func (r *Runner) replyImageOp(ctx context.Context, outbound chan<- *agentv1.AgentMessage, requestID, imageID string, reclaimedBytes int64, err error) {
	select {
	case outbound <- imageOpResultMessage(requestID, imageID, reclaimedBytes, err):
	case <-ctx.Done():
	}
}

// containerConfigFromProto converts the wire ContainerConfig into
// docker.ContainerConfig, which ops.go's Create/Recreate functions operate
// on — keeps the docker package's config type free of agentv1, matching
// how ContainerDetail (inspect.go) is already kept separate from its proto
// counterpart.
func containerConfigFromProto(cfg *agentv1.ContainerConfig) docker.ContainerConfig {
	ports := make([]docker.ContainerPortSpec, 0, len(cfg.GetPorts()))
	for _, p := range cfg.GetPorts() {
		ports = append(ports, docker.ContainerPortSpec{
			ContainerPort: uint16(p.GetContainerPort()),
			HostPort:      uint16(p.GetHostPort()),
			Protocol:      p.GetProtocol(),
		})
	}
	volumes := make([]docker.ContainerVolumeSpec, 0, len(cfg.GetVolumes()))
	for _, v := range cfg.GetVolumes() {
		volumes = append(volumes, docker.ContainerVolumeSpec{
			VolumeName: v.GetVolumeName(),
			Target:     v.GetTarget(),
			ReadOnly:   v.GetReadOnly(),
		})
	}
	return docker.ContainerConfig{
		Image:                      cfg.GetImage(),
		Name:                       cfg.GetName(),
		Command:                    cfg.GetCommand(),
		Env:                        cfg.GetEnv(),
		Ports:                      ports,
		Volumes:                    volumes,
		RestartPolicyName:          cfg.GetRestartPolicyName(),
		RestartPolicyMaxRetryCount: int(cfg.GetRestartPolicyMaxRetryCount()),
		Labels:                     cfg.GetLabels(),
		NanoCPUs:                   cfg.GetNanoCpus(),
		MemoryLimitBytes:           cfg.GetMemoryLimitBytes(),
		MemoryReservationBytes:     cfg.GetMemoryReservationBytes(),
		PidsLimit:                  cfg.GetPidsLimit(),
	}
}

func uploadBlob(ctx context.Context, client *http.Client, url, credential string, body io.Reader) error {
	if err := validateBackupURL(url); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/x-tar")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed: %s: %s", resp.Status, string(respBody))
	}
	return nil
}

func downloadBlob(ctx context.Context, client *http.Client, url, credential string) (io.ReadCloser, error) {
	if err := validateBackupURL(url); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credential)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed: %s: %s", resp.Status, string(respBody))
	}
	return resp.Body, nil
}

// heartbeatMessage samples current host resources every tick, but only
// lists containers (a live Docker Engine API call) every containerListEvery
// ticks — the expensive part of a heartbeat on constrained hardware, gated
// independently of the cheap resource sampling (see edge-device tuning's
// low-memory metrics collection). Either collector failing is logged and
// degrades that part of the heartbeat to its zero value — a heartbeat must
// still go out on schedule even if one metric source is temporarily
// unavailable.
func (r *Runner) heartbeatMessage(ctx context.Context, dockerCli *dockerclient.Client, serverID string, tick int) *agentv1.AgentMessage {
	snap, err := health.Collect(ctx)
	if err != nil {
		r.log.Warn("failed to collect host metrics", "error", err)
	}

	containerListEvery := r.containerListEvery
	if containerListEvery < 1 {
		containerListEvery = standardContainerListEvery
	}
	refreshContainers := tick%containerListEvery == 0

	var containers []*agentv1.ContainerSummary
	var containerStats []*agentv1.ContainerResourceUsage
	if refreshContainers && dockerCli != nil {
		list, err := docker.ListContainers(ctx, dockerCli)
		if err != nil {
			r.log.Warn("failed to list containers for heartbeat", "error", err)
		}
		containers = make([]*agentv1.ContainerSummary, len(list))
		for i, c := range list {
			containers[i] = &agentv1.ContainerSummary{
				Id:           c.ID,
				Name:         c.Name,
				State:        c.State,
				DeploymentId: c.DeploymentID,
				Image:        c.Image,
				ImageId:      c.ImageID,
				CreatedUnix:  c.CreatedUnix,
				Status:       c.Status,
				Ports:        mapContainerPorts(c.Ports),
				Networks:     mapContainerNetworks(c.Networks),
				Mounts:       mapContainerMounts(c.Mounts),
			}
		}
		containerStats = r.collectContainerStats(ctx, dockerCli, list)
	}

	return buildHeartbeat(serverID, time.Now(), snap, containers, refreshContainers, containerStats, refreshContainers)
}

// containerStatsTimeout bounds each container's stats call so one stuck
// cgroup read can't stall the whole heartbeat.
const containerStatsTimeout = 5 * time.Second

// collectContainerStats samples resource usage for every running container
// in list, concurrently (each ContainerStats call briefly blocks daemon-side
// to compute a CPU delta, so doing them one at a time would multiply that
// wait by the container count). Rides the same refresh cadence as the
// container list itself — see heartbeatMessage's doc comment.
func (r *Runner) collectContainerStats(ctx context.Context, dockerCli *dockerclient.Client, list []docker.ContainerSummary) []*agentv1.ContainerResourceUsage {
	type result struct {
		id    string
		stats docker.ContainerStats
		err   error
	}

	var running []docker.ContainerSummary
	for _, c := range list {
		if c.State == "running" {
			running = append(running, c)
		}
	}
	if len(running) == 0 {
		return nil
	}

	results := make(chan result, len(running))
	for _, c := range running {
		go func(id string) {
			statsCtx, cancel := context.WithTimeout(ctx, containerStatsTimeout)
			defer cancel()
			stats, err := docker.CollectContainerStats(statsCtx, dockerCli, id)
			results <- result{id: id, stats: stats, err: err}
		}(c.ID)
	}

	out := make([]*agentv1.ContainerResourceUsage, 0, len(running))
	for range running {
		res := <-results
		if res.err != nil {
			r.log.Warn("failed to collect container stats", "container_id", res.id, "error", res.err)
			continue
		}
		out = append(out, &agentv1.ContainerResourceUsage{
			ContainerId:     res.id,
			CpuPercent:      res.stats.CPUPercent,
			MemUsageBytes:   res.stats.MemUsageBytes,
			MemLimitBytes:   res.stats.MemLimitBytes,
			MemPercent:      res.stats.MemPercent,
			NetRxBytes:      res.stats.NetRxBytes,
			NetTxBytes:      res.stats.NetTxBytes,
			BlockReadBytes:  res.stats.BlockReadBytes,
			BlockWriteBytes: res.stats.BlockWriteBytes,
			Pids:            res.stats.PIDs,
		})
	}
	return out
}

func mapContainerPorts(ports []docker.ContainerPort) []*agentv1.ContainerPort {
	out := make([]*agentv1.ContainerPort, len(ports))
	for i, p := range ports {
		out[i] = &agentv1.ContainerPort{
			Ip:          p.IP,
			PrivatePort: uint32(p.PrivatePort),
			PublicPort:  uint32(p.PublicPort),
			Type:        p.Type,
		}
	}
	return out
}

func mapContainerNetworks(networks []docker.ContainerNetwork) []*agentv1.ContainerNetwork {
	out := make([]*agentv1.ContainerNetwork, len(networks))
	for i, n := range networks {
		out[i] = &agentv1.ContainerNetwork{Name: n.Name, IpAddress: n.IPAddress}
	}
	return out
}

func mapContainerMounts(mounts []docker.ContainerMount) []*agentv1.ContainerMount {
	out := make([]*agentv1.ContainerMount, len(mounts))
	for i, m := range mounts {
		out[i] = &agentv1.ContainerMount{
			Type:        m.Type,
			Name:        m.Name,
			Source:      m.Source,
			Destination: m.Destination,
			ReadWrite:   m.ReadWrite,
		}
	}
	return out
}

// buildHeartbeat assembles a Heartbeat AgentMessage from already-collected
// data — shared by the live path above and flushBufferedHeartbeats, which
// replays historical samples with no live container refresh.
func buildHeartbeat(serverID string, sentAt time.Time, snap health.Snapshot, containers []*agentv1.ContainerSummary, containersIncluded bool, containerStats []*agentv1.ContainerResourceUsage, containerStatsIncluded bool) *agentv1.AgentMessage {
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_Heartbeat{
			Heartbeat: &agentv1.Heartbeat{
				ServerId: serverID,
				SentAt:   timestamppb.New(sentAt),
				Resources: &agentv1.ResourceSnapshot{
					CpuPercent:       snap.CPUPercent,
					MemPercent:       snap.MemPercent,
					DiskPercent:      snap.DiskPercent,
					TotalMemoryBytes: snap.TotalMemoryBytes,
					NumCpus:          snap.NumCPUs,
					TotalDiskBytes:   snap.TotalDiskBytes,
				},
				Containers:             containers,
				ContainersIncluded:     containersIncluded,
				ContainerStats:         containerStats,
				ContainerStatsIncluded: containerStatsIncluded,
			},
		},
	}
}

func deployStatusMessage(deploymentID string, revision int32, phase agentv1.DeployPhase, service, message string) *agentv1.AgentMessage {
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_DeployStatus{
			DeployStatus: &agentv1.DeployStatus{
				DeploymentId: deploymentID,
				Revision:     revision,
				Phase:        phase,
				Service:      service,
				Message:      message,
				UpdatedAt:    timestamppb.Now(),
			},
		},
	}
}

func backupStatusMessage(backupID string, phase agentv1.TaskPhase, message string) *agentv1.AgentMessage {
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_BackupStatus{
			BackupStatus: &agentv1.BackupStatus{
				BackupId: backupID,
				Phase:    phase,
				Message:  message,
			},
		},
	}
}

func restoreStatusMessage(backupID, deploymentID string, phase agentv1.TaskPhase, message string) *agentv1.AgentMessage {
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_RestoreStatus{
			RestoreStatus: &agentv1.RestoreStatus{
				BackupId:     backupID,
				DeploymentId: deploymentID,
				Phase:        phase,
				Message:      message,
			},
		},
	}
}

func containerDetailMessage(requestID, containerID string, d docker.ContainerDetail) *agentv1.AgentMessage {
	healthLog := make([]*agentv1.HealthCheckEntry, len(d.HealthLog))
	for i, h := range d.HealthLog {
		healthLog[i] = &agentv1.HealthCheckEntry{
			StartUnix: h.Start.Unix(),
			EndUnix:   h.End.Unix(),
			ExitCode:  int32(h.ExitCode),
			Output:    h.Output,
		}
	}
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_ContainerDetail{
			ContainerDetail: &agentv1.ContainerDetail{
				RequestId:                  requestID,
				ContainerId:                containerID,
				Found:                      d.Found,
				ErrorMessage:               d.ErrorMessage,
				Env:                        d.Env,
				RestartPolicyName:          d.RestartPolicyName,
				RestartPolicyMaxRetryCount: int32(d.RestartPolicyMaxRetryCount),
				HealthStatus:               d.HealthStatus,
				HealthFailingStreak:        int32(d.HealthFailingStreak),
				RestartCount:               int32(d.RestartCount),
				HealthLog:                  healthLog,
				Command:                    d.Command,
				Entrypoint:                 d.Entrypoint,
				WorkingDir:                 d.WorkingDir,
				Labels:                     d.Labels,
				Image:                      d.Image,
				NanoCpus:                   d.NanoCPUs,
				MemoryLimitBytes:           d.MemoryLimitBytes,
				MemoryReservationBytes:     d.MemoryReservationBytes,
				PidsLimit:                  d.PidsLimit,
			},
		},
	}
}

func containerOpResultMessage(requestID, containerID string, err error) *agentv1.AgentMessage {
	result := &agentv1.ContainerOpResult{
		RequestId:   requestID,
		Success:     err == nil,
		ContainerId: containerID,
	}
	if err != nil {
		result.ErrorMessage = err.Error()
	}
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_ContainerOpResult{ContainerOpResult: result},
	}
}

func imageListResultMessage(requestID string, images []docker.ImageSummary) *agentv1.AgentMessage {
	out := make([]*agentv1.ImageSummary, len(images))
	for i, img := range images {
		out[i] = &agentv1.ImageSummary{
			Id:              img.ID,
			RepoTags:        img.RepoTags,
			RepoDigests:     img.RepoDigests,
			SizeBytes:       img.SizeBytes,
			CreatedUnix:     img.CreatedUnix,
			Dangling:        img.Dangling,
			ContainersCount: img.ContainersCount,
		}
	}
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_ImageListResult{
			ImageListResult: &agentv1.ImageListResult{RequestId: requestID, Images: out},
		},
	}
}

func imageDetailMessage(requestID string, d docker.ImageDetail) *agentv1.AgentMessage {
	layers := make([]*agentv1.ImageLayer, len(d.Layers))
	for i, l := range d.Layers {
		layers[i] = &agentv1.ImageLayer{Digest: l.Digest, SizeBytes: l.SizeBytes}
	}
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_ImageDetail{
			ImageDetail: &agentv1.ImageDetail{
				RequestId:    requestID,
				Found:        d.Found,
				ErrorMessage: d.ErrorMessage,
				Id:           d.ID,
				RepoTags:     d.RepoTags,
				RepoDigests:  d.RepoDigests,
				SizeBytes:    d.SizeBytes,
				CreatedUnix:  d.CreatedUnix,
				Architecture: d.Architecture,
				Os:           d.OS,
				Layers:       layers,
				Env:          d.Env,
				Labels:       d.Labels,
			},
		},
	}
}

func imageOpResultMessage(requestID, imageID string, reclaimedBytes int64, err error) *agentv1.AgentMessage {
	result := &agentv1.ImageOpResult{
		RequestId:      requestID,
		Success:        err == nil,
		ImageId:        imageID,
		ReclaimedBytes: reclaimedBytes,
	}
	if err != nil {
		result.ErrorMessage = err.Error()
	}
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_ImageOpResult{ImageOpResult: result},
	}
}
