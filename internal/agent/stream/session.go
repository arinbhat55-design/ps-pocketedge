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
	"math/rand"
	"net/http"
	"time"

	dockerclient "github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/docker"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/enroll"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/health"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/state"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

const (
	heartbeatInterval = 20 * time.Second
	minBackoff        = 1 * time.Second
	maxBackoff        = 60 * time.Second
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

	backoff := minBackoff
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := r.runSession(ctx, client, identity, dockerCli); err != nil {
			r.log.Warn("session ended, reconnecting", "error", err, "backoff", backoff)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jitter(backoff)):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
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
			}
		}
	}()

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	// Send an immediate heartbeat on connect rather than waiting a full
	// interval, so a fresh/reconnected agent shows up promptly.
	if err := stream.Send(r.heartbeatMessage(ctx, dockerCli, identity.ServerID)); err != nil {
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
			if err := stream.Send(r.heartbeatMessage(ctx, dockerCli, identity.ServerID)); err != nil {
				return err
			}
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

// heartbeatMessage samples current host resources and (if a Docker daemon
// is reachable) the container inventory. Either collector failing is logged
// and degrades that part of the heartbeat to its zero value — a heartbeat
// must still go out on schedule even if one metric source is temporarily
// unavailable.
func (r *Runner) heartbeatMessage(ctx context.Context, dockerCli *dockerclient.Client, serverID string) *agentv1.AgentMessage {
	snap, err := health.Collect(ctx)
	if err != nil {
		r.log.Warn("failed to collect host metrics", "error", err)
	}

	var containers []*agentv1.ContainerSummary
	if dockerCli != nil {
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
			}
		}
	}

	return &agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_Heartbeat{
			Heartbeat: &agentv1.Heartbeat{
				ServerId: serverID,
				SentAt:   timestamppb.Now(),
				Resources: &agentv1.ResourceSnapshot{
					CpuPercent:  snap.CPUPercent,
					MemPercent:  snap.MemPercent,
					DiskPercent: snap.DiskPercent,
				},
				Containers: containers,
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

func jitter(d time.Duration) time.Duration {
	//nolint:gosec // non-cryptographic jitter is fine here
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}
