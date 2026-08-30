// Package stream manages the agent's persistent connection to the control
// plane: enrolling once, then keeping the bidirectional Session stream open
// with reconnect/backoff, periodic heartbeats, and applying any
// DeployStackCommand the control plane sends.
package stream

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	dockerclient "github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

	log *slog.Logger

	// heartbeatInterval/containerListEvery are set once in Run, from the
	// device's detected tier (see detectTier) — every session and the
	// disconnected-buffering loop both read them.
	heartbeatInterval  time.Duration
	containerListEvery int
	// bufferPath is where the offline heartbeat buffer lives, derived from
	// StatePath; empty (buffering disabled) if StatePath is unset.
	bufferPath string
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
	conn, err := grpc.NewClient(r.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

	recvErrCh := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				recvErrCh <- err
				return
			}
			switch {
			case msg.GetDeployStack() != nil:
				go r.handleDeploy(sessionCtx, dockerCli, msg.GetDeployStack(), outbound)
			case msg.GetBackup() != nil:
				go r.handleBackup(sessionCtx, dockerCli, msg.GetBackup(), identity.Credential, outbound)
			case msg.GetRestore() != nil:
				go r.handleRestore(sessionCtx, dockerCli, msg.GetRestore(), identity.Credential, outbound)
			case msg.GetInspectContainer() != nil:
				go r.handleInspectContainer(sessionCtx, dockerCli, msg.GetInspectContainer(), outbound)
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
		}, nil, false)
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
	report := func(phase agentv1.DeployPhase, message string) {
		select {
		case outbound <- deployStatusMessage(cmd.GetDeploymentId(), phase, message):
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		r.log.Error("cannot deploy, no docker client available", "deployment_id", cmd.GetDeploymentId())
		report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "docker client unavailable on this agent")
		return
	}

	r.log.Info("deploying stack", "deployment_id", cmd.GetDeploymentId(), "stack", cmd.GetStackName())
	if err := docker.Deploy(ctx, dockerCli, cmd, report); err != nil {
		r.log.Error("deploy failed", "deployment_id", cmd.GetDeploymentId(), "error", err)
		return
	}
	r.log.Info("deploy succeeded", "deployment_id", cmd.GetDeploymentId())
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

	report(agentv1.TaskPhase_TASK_PHASE_RUNNING, "snapshotting volumes")

	reader, err := docker.BackupVolumes(ctx, dockerCli, cmd.GetDeploymentId())
	if err != nil {
		r.log.Error("backup snapshot failed", "backup_id", cmd.GetBackupId(), "error", err)
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "failed to snapshot volumes: "+err.Error())
		return
	}
	defer reader.Close()

	if err := uploadBlob(ctx, cmd.GetUploadUrl(), credential, reader); err != nil {
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

	body, err := downloadBlob(ctx, cmd.GetDownloadUrl(), credential)
	if err != nil {
		r.log.Error("restore download failed", "backup_id", cmd.GetBackupId(), "error", err)
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "failed to download backup: "+err.Error())
		return
	}
	defer body.Close()

	if err := docker.RestoreVolumes(ctx, dockerCli, cmd, body); err != nil {
		r.log.Error("restore extraction failed", "backup_id", cmd.GetBackupId(), "error", err)
		report(agentv1.TaskPhase_TASK_PHASE_FAILED, "failed to restore volumes: "+err.Error())
		return
	}
	report(agentv1.TaskPhase_TASK_PHASE_COMPLETED, "")

	r.log.Info("restored volumes, redeploying", "backup_id", cmd.GetBackupId(), "deployment_id", cmd.GetDeploymentId())
	r.handleDeploy(ctx, dockerCli, &agentv1.DeployStackCommand{
		DeploymentId: cmd.GetDeploymentId(),
		StackName:    cmd.GetStackName(),
		ComposeYaml:  cmd.GetComposeYaml(),
		Env:          cmd.GetEnv(),
	}, outbound)
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

func uploadBlob(ctx context.Context, url, credential string, body io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/x-tar")

	resp, err := http.DefaultClient.Do(req)
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

func downloadBlob(ctx context.Context, url, credential string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credential)

	resp, err := http.DefaultClient.Do(req)
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
	}

	return buildHeartbeat(serverID, time.Now(), snap, containers, refreshContainers)
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
func buildHeartbeat(serverID string, sentAt time.Time, snap health.Snapshot, containers []*agentv1.ContainerSummary, containersIncluded bool) *agentv1.AgentMessage {
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_Heartbeat{
			Heartbeat: &agentv1.Heartbeat{
				ServerId: serverID,
				SentAt:   timestamppb.New(sentAt),
				Resources: &agentv1.ResourceSnapshot{
					CpuPercent:  snap.CPUPercent,
					MemPercent:  snap.MemPercent,
					DiskPercent: snap.DiskPercent,
				},
				Containers:         containers,
				ContainersIncluded: containersIncluded,
			},
		},
	}
}

func deployStatusMessage(deploymentID string, phase agentv1.DeployPhase, message string) *agentv1.AgentMessage {
	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_DeployStatus{
			DeployStatus: &agentv1.DeployStatus{
				DeploymentId: deploymentID,
				Phase:        phase,
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
			},
		},
	}
}
