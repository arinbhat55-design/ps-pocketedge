// Package grpcserver implements the control-plane side of the AgentSession
// gRPC service: agent enrollment and the persistent heartbeat/command stream.
package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/livestate"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// Server implements agentv1.AgentSessionServer.
type Server struct {
	agentv1.UnimplementedAgentSessionServer

	log                *slog.Logger
	store              *store.Store
	dispatcher         *deploy.Dispatcher
	events             *deploy.EventBus
	serverEvents       *livestate.EventBus
	inspectWaiter      *deploy.InspectWaiter
	opWaiter           *deploy.OpWaiter
	imageListWaiter    *deploy.ImageListWaiter
	imageDetailWaiter  *deploy.ImageDetailWaiter
	imageOpWaiter      *deploy.ImageOpWaiter
	logStreamRelay     *deploy.LogStreamRelay
	eventListWaiter    *deploy.EventListWaiter
	execStreamRelay    *deploy.ExecStreamRelay
	networkListWaiter  *deploy.NetworkListWaiter
	networkOpWaiter    *deploy.NetworkOpWaiter
	volumeListWaiter   *deploy.VolumeListWaiter
	volumeDetailWaiter *deploy.VolumeDetailWaiter
	volumeOpWaiter     *deploy.VolumeOpWaiter
	volumeFileWaiter   *deploy.VolumeFileWaiter
	buildBus           *deploy.BuildBus
}

func New(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus, serverEvents *livestate.EventBus, inspectWaiter *deploy.InspectWaiter, opWaiter *deploy.OpWaiter, imageListWaiter *deploy.ImageListWaiter, imageDetailWaiter *deploy.ImageDetailWaiter, imageOpWaiter *deploy.ImageOpWaiter, logStreamRelay *deploy.LogStreamRelay, eventListWaiter *deploy.EventListWaiter, execStreamRelay *deploy.ExecStreamRelay, networkListWaiter *deploy.NetworkListWaiter, networkOpWaiter *deploy.NetworkOpWaiter, volumeListWaiter *deploy.VolumeListWaiter, volumeDetailWaiter *deploy.VolumeDetailWaiter, volumeOpWaiter *deploy.VolumeOpWaiter, volumeFileWaiter *deploy.VolumeFileWaiter, buildBus *deploy.BuildBus) *Server {
	return &Server{
		log: log, store: st, dispatcher: dispatcher, events: events, serverEvents: serverEvents,
		inspectWaiter: inspectWaiter, opWaiter: opWaiter,
		imageListWaiter: imageListWaiter, imageDetailWaiter: imageDetailWaiter, imageOpWaiter: imageOpWaiter,
		logStreamRelay: logStreamRelay, eventListWaiter: eventListWaiter, execStreamRelay: execStreamRelay,
		networkListWaiter: networkListWaiter, networkOpWaiter: networkOpWaiter,
		volumeListWaiter: volumeListWaiter, volumeDetailWaiter: volumeDetailWaiter, volumeOpWaiter: volumeOpWaiter, volumeFileWaiter: volumeFileWaiter,
		buildBus: buildBus,
	}
}

func (s *Server) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	if req.GetToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	tokenHash := auth.HashToken(req.GetToken())
	if err := s.store.ConsumeEnrollmentToken(ctx, tokenHash); err != nil {
		if errors.Is(err, store.ErrTokenInvalid) {
			return nil, status.Error(codes.Unauthenticated, "enrollment token invalid or expired")
		}
		s.log.Error("failed to consume enrollment token", "error", err)
		return nil, status.Error(codes.Internal, "failed to validate enrollment token")
	}

	credential, err := auth.RandomToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate agent credential")
	}

	serverID, err := s.store.CreateServer(ctx, req.GetHostname(), req.GetOs(), req.GetArch(), req.GetAgentVersion(), auth.HashToken(credential), nil)
	if err != nil {
		s.log.Error("failed to persist enrolled server", "error", err)
		return nil, status.Error(codes.Internal, "failed to persist server")
	}

	if err := s.store.LinkEnrollmentTokenToServer(ctx, tokenHash, serverID); err != nil {
		// Non-fatal: the server is already created and enrolled; this is
		// just an audit-trail link, so log and continue.
		s.log.Warn("failed to link enrollment token to server", "server_id", serverID, "error", err)
	}

	s.log.Info("agent enrolled",
		"server_id", serverID,
		"hostname", req.GetHostname(),
		"os", req.GetOs(),
		"arch", req.GetArch(),
		"agent_version", req.GetAgentVersion(),
	)

	return &agentv1.EnrollResponse{
		ServerId:        serverID,
		AgentCredential: credential,
	}, nil
}

func (s *Server) Session(stream agentv1.AgentSession_SessionServer) error {
	ctx := stream.Context()

	serverID, err := s.authenticate(ctx)
	if err != nil {
		return err
	}

	// Register this connection so REST-triggered deployments (internal/
	// controlplane/deploy) can route commands to this specific agent. The
	// send side runs in its own goroutine since the stream is
	// bidirectional and stream.Recv() below blocks.
	outbound, unregister := s.dispatcher.Register(serverID)
	defer unregister()

	sendErrCh := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-outbound:
				if err := stream.Send(msg); err != nil {
					sendErrCh <- err
					return
				}
			}
		}
	}()

	s.log.Info("agent connected", "server_id", serverID)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sendErrCh:
			s.log.Info("agent session closed (send failed)", "server_id", serverID, "error", err)
			return err
		default:
		}

		msg, err := stream.Recv()
		if err != nil {
			s.log.Info("agent session closed", "server_id", serverID, "error", err)
			return err
		}

		switch payload := msg.Payload.(type) {
		case *agentv1.AgentMessage_Heartbeat:
			hb := payload.Heartbeat
			if hb.GetServerId() != serverID {
				s.log.Warn("heartbeat server_id does not match authenticated credential", "authenticated", serverID, "claimed", hb.GetServerId())
				continue
			}
			resources := store.ResourceSnapshot{
				CPUPercent:       hb.GetResources().GetCpuPercent(),
				MemPercent:       hb.GetResources().GetMemPercent(),
				DiskPercent:      hb.GetResources().GetDiskPercent(),
				TotalMemoryBytes: hb.GetResources().GetTotalMemoryBytes(),
				NumCPUs:          hb.GetResources().GetNumCpus(),
				TotalDiskBytes:   hb.GetResources().GetTotalDiskBytes(),
			}
			if err := s.store.RecordHeartbeat(ctx, serverID, resources); err != nil {
				s.log.Error("failed to record heartbeat", "server_id", serverID, "error", err)
				continue
			}

			heartbeatTime := time.Now()
			recordedAt := heartbeatTime
			if sentAt := hb.GetSentAt(); sentAt != nil {
				// Buffered/replayed samples from the agent's offline buffer
				// carry their original sample time here, not "now" — see
				// edge-device tuning's offline heartbeat buffer.
				recordedAt = sentAt.AsTime()
			}
			if err := s.store.InsertMetricSample(ctx, serverID, recordedAt, resources); err != nil {
				s.log.Error("failed to record metric sample", "server_id", serverID, "error", err)
			}

			// containers_included is false on a tick the agent deliberately
			// skipped refreshing (edge-device tuning's container-list
			// gating, cheaper than a Docker Engine API call on constrained
			// hardware) — the last-known inventory is read back for the
			// live-push payload below instead of being replaced.
			var containers []store.ContainerState
			if hb.GetContainersIncluded() {
				containers = make([]store.ContainerState, len(hb.GetContainers()))
				for i, c := range hb.GetContainers() {
					var deploymentID *string
					if id := c.GetDeploymentId(); id != "" {
						deploymentID = &id
					}
					var createdAt *time.Time
					if unix := c.GetCreatedUnix(); unix > 0 {
						t := time.Unix(unix, 0)
						createdAt = &t
					}
					containers[i] = store.ContainerState{
						ContainerID:  c.GetId(),
						Name:         c.GetName(),
						State:        c.GetState(),
						DeploymentID: deploymentID,
						Image:        c.GetImage(),
						ImageID:      c.GetImageId(),
						CreatedAt:    createdAt,
						Status:       c.GetStatus(),
						Ports:        mapContainerPorts(c.GetPorts()),
						Networks:     mapContainerNetworks(c.GetNetworks()),
						Mounts:       mapContainerMounts(c.GetMounts()),
						UpdatedAt:    heartbeatTime,
					}
				}
				if err := s.store.ReplaceContainers(ctx, serverID, containers); err != nil {
					s.log.Error("failed to replace container inventory", "server_id", serverID, "error", err)
				}
			} else if existing, err := s.store.ListContainers(ctx, serverID); err != nil {
				s.log.Error("failed to load last-known container inventory", "server_id", serverID, "error", err)
			} else {
				containers = existing
			}

			// container_stats_included rides the same refresh tick as
			// containers (see agent/stream/session.go's collectContainerStats)
			// — there's no "last-known" fallback the way containers has one,
			// since these are time-series samples, not current state: a tick
			// that doesn't include them just contributes nothing new to the
			// history, and the live push carries an empty slice.
			var containerStats []store.ContainerResourceUsage
			if hb.GetContainerStatsIncluded() {
				containerStats = make([]store.ContainerResourceUsage, len(hb.GetContainerStats()))
				for i, u := range hb.GetContainerStats() {
					usage := store.ContainerResourceUsage{
						ContainerID:     u.GetContainerId(),
						RecordedAt:      recordedAt,
						CPUPercent:      u.GetCpuPercent(),
						MemUsageBytes:   u.GetMemUsageBytes(),
						MemLimitBytes:   u.GetMemLimitBytes(),
						MemPercent:      u.GetMemPercent(),
						NetRxBytes:      u.GetNetRxBytes(),
						NetTxBytes:      u.GetNetTxBytes(),
						BlockReadBytes:  u.GetBlockReadBytes(),
						BlockWriteBytes: u.GetBlockWriteBytes(),
						PIDs:            u.GetPids(),
					}
					containerStats[i] = usage
					if err := s.store.InsertContainerMetricSample(ctx, serverID, recordedAt, usage); err != nil {
						s.log.Error("failed to record container metric sample", "server_id", serverID, "container_id", usage.ContainerID, "error", err)
					}
				}
			}

			s.log.Info("heartbeat recorded",
				"server_id", serverID,
				"cpu_percent", resources.CPUPercent,
				"mem_percent", resources.MemPercent,
				"disk_percent", resources.DiskPercent,
				"containers", len(containers),
				"containers_included", hb.GetContainersIncluded(),
			)

			s.serverEvents.Publish(serverID, livestate.ServerUpdate{
				Resources:      resources,
				Containers:     containers,
				ContainerStats: containerStats,
				UpdatedAt:      heartbeatTime,
			})
		case *agentv1.AgentMessage_DeployStatus:
			ds := payload.DeployStatus
			phase := deployPhaseToString(ds.GetPhase())
			s.log.Info("deploy status received",
				"server_id", serverID,
				"deployment_id", ds.GetDeploymentId(),
				"service", ds.GetService(),
				"phase", phase,
				"message", ds.GetMessage(),
			)
			s.recordDeployStatus(ctx, ds.GetDeploymentId(), int(ds.GetRevision()), ds.GetService(), phase, ds.GetMessage())
		case *agentv1.AgentMessage_BackupStatus:
			bs := payload.BackupStatus
			phase := taskPhaseToString(bs.GetPhase())
			s.log.Info("backup status received",
				"server_id", serverID,
				"backup_id", bs.GetBackupId(),
				"phase", phase,
				"message", bs.GetMessage(),
			)
			// COMPLETED is set by the blob-upload HTTP handler once the
			// bytes actually land (internal/controlplane/api), not here —
			// this stream-level status only ever reports RUNNING/FAILED.
			if phase == "failed" {
				if err := s.store.UpdateBackupStatus(ctx, bs.GetBackupId(), phase, bs.GetMessage()); err != nil {
					s.log.Error("failed to update backup status", "backup_id", bs.GetBackupId(), "error", err)
				}
			} else if phase == "running" {
				_ = s.store.UpdateBackupStatus(ctx, bs.GetBackupId(), phase, bs.GetMessage())
			}
		case *agentv1.AgentMessage_RestoreStatus:
			rs := payload.RestoreStatus
			phase := taskPhaseToString(rs.GetPhase())
			s.log.Info("restore status received",
				"server_id", serverID,
				"backup_id", rs.GetBackupId(),
				"deployment_id", rs.GetDeploymentId(),
				"phase", phase,
				"message", rs.GetMessage(),
			)
			switch phase {
			case "running":
				if event, err := s.store.AddDeploymentEvent(ctx, rs.GetDeploymentId(), "restoring", rs.GetMessage(), ""); err == nil {
					s.events.Publish(rs.GetDeploymentId(), event)
				}
			case "failed":
				_ = s.store.UpdateDeploymentPhase(ctx, rs.GetDeploymentId(), "failed")
				if event, err := s.store.AddDeploymentEvent(ctx, rs.GetDeploymentId(), "failed", rs.GetMessage(), ""); err == nil {
					s.events.Publish(rs.GetDeploymentId(), event)
				}
			}
			if phase == "completed" || phase == "failed" {
				if err := s.store.SetDatabaseCloneJobStatusByRestore(ctx, rs.GetDeploymentId(), rs.GetBackupId(), phase, rs.GetMessage()); err != nil {
					s.log.Error("failed to update clone status", "deployment_id", rs.GetDeploymentId(), "error", err)
				}
			}
			// COMPLETED needs no event here: the agent immediately follows
			// up with a normal deploy pipeline run, whose DeployStatus
			// events (handled above) take over the deployment's status
			// feed from this point.
		case *agentv1.AgentMessage_ContainerDetail:
			cd := payload.ContainerDetail
			s.log.Info("container detail received",
				"server_id", serverID,
				"container_id", cd.GetContainerId(),
				"found", cd.GetFound(),
			)
			s.inspectWaiter.Deliver(cd.GetRequestId(), cd)
		case *agentv1.AgentMessage_ContainerOpResult:
			result := payload.ContainerOpResult
			s.log.Info("container op result received",
				"server_id", serverID,
				"container_id", result.GetContainerId(),
				"success", result.GetSuccess(),
			)
			s.opWaiter.Deliver(result.GetRequestId(), result)
		case *agentv1.AgentMessage_ImageListResult:
			result := payload.ImageListResult
			s.log.Info("image list result received", "server_id", serverID, "count", len(result.GetImages()))
			s.imageListWaiter.Deliver(result.GetRequestId(), result)
		case *agentv1.AgentMessage_ImageDetail:
			detail := payload.ImageDetail
			s.log.Info("image detail received", "server_id", serverID, "image_id", detail.GetId(), "found", detail.GetFound())
			s.imageDetailWaiter.Deliver(detail.GetRequestId(), detail)
		case *agentv1.AgentMessage_ImageOpResult:
			result := payload.ImageOpResult
			s.log.Info("image op result received", "server_id", serverID, "image_id", result.GetImageId(), "success", result.GetSuccess())
			s.imageOpWaiter.Deliver(result.GetRequestId(), result)
		case *agentv1.AgentMessage_LogChunk:
			s.logStreamRelay.Publish(payload.LogChunk.GetRequestId(), payload.LogChunk)
		case *agentv1.AgentMessage_EventListResult:
			result := payload.EventListResult
			s.log.Info("event list result received", "server_id", serverID, "count", len(result.GetEvents()))
			s.eventListWaiter.Deliver(result.GetRequestId(), result)
		case *agentv1.AgentMessage_ExecOutput:
			s.execStreamRelay.Publish(payload.ExecOutput.GetRequestId(), payload.ExecOutput)
		case *agentv1.AgentMessage_NetworkListResult:
			result := payload.NetworkListResult
			s.log.Info("network list result received", "server_id", serverID, "count", len(result.GetNetworks()))
			s.networkListWaiter.Deliver(result.GetRequestId(), result)
		case *agentv1.AgentMessage_NetworkOpResult:
			result := payload.NetworkOpResult
			s.log.Info("network op result received", "server_id", serverID, "network_id", result.GetNetworkId(), "success", result.GetSuccess())
			s.networkOpWaiter.Deliver(result.GetRequestId(), result)
		case *agentv1.AgentMessage_VolumeListResult:
			result := payload.VolumeListResult
			s.log.Info("volume list result received", "server_id", serverID, "count", len(result.GetVolumes()))
			s.volumeListWaiter.Deliver(result.GetRequestId(), result)
		case *agentv1.AgentMessage_VolumeDetail:
			detail := payload.VolumeDetail
			s.log.Info("volume detail received", "server_id", serverID, "name", detail.GetVolume().GetName(), "found", detail.GetFound())
			s.volumeDetailWaiter.Deliver(detail.GetRequestId(), detail)
		case *agentv1.AgentMessage_VolumeOpResult:
			result := payload.VolumeOpResult
			s.log.Info("volume op result received", "server_id", serverID, "name", result.GetName(), "success", result.GetSuccess())
			s.volumeOpWaiter.Deliver(result.GetRequestId(), result)
		case *agentv1.AgentMessage_VolumeFileResult:
			result := payload.VolumeFileResult
			s.volumeFileWaiter.Deliver(result.GetRequestId(), result)
		case *agentv1.AgentMessage_BuildStatus:
			s.recordBuildStatus(ctx, serverID, payload.BuildStatus)
		default:
			s.log.Warn("unknown agent message payload", "server_id", serverID)
		}
	}
}

func mapContainerPorts(ports []*agentv1.ContainerPort) []store.ContainerPort {
	if len(ports) == 0 {
		return nil
	}
	out := make([]store.ContainerPort, len(ports))
	for i, p := range ports {
		out[i] = store.ContainerPort{
			IP:          p.GetIp(),
			PrivatePort: uint16(p.GetPrivatePort()),
			PublicPort:  uint16(p.GetPublicPort()),
			Type:        p.GetType(),
		}
	}
	return out
}

func mapContainerNetworks(networks []*agentv1.ContainerNetwork) []store.ContainerNetwork {
	if len(networks) == 0 {
		return nil
	}
	out := make([]store.ContainerNetwork, len(networks))
	for i, n := range networks {
		out[i] = store.ContainerNetwork{Name: n.GetName(), IPAddress: n.GetIpAddress()}
	}
	return out
}

func mapContainerMounts(mounts []*agentv1.ContainerMount) []store.ContainerMount {
	if len(mounts) == 0 {
		return nil
	}
	out := make([]store.ContainerMount, len(mounts))
	for i, m := range mounts {
		out[i] = store.ContainerMount{
			Type:        m.GetType(),
			Name:        m.GetName(),
			Source:      m.GetSource(),
			Destination: m.GetDestination(),
			ReadWrite:   m.GetReadWrite(),
		}
	}
	return out
}

// recordDeployStatus applies one agent-reported deploy status:
//   - a per-service status (service set) is only appended to the timeline —
//     it never changes the deployment's overall phase;
//   - running/failed/rolled_back/healthy/unhealthy update the status of the
//     revision the status belongs to;
//   - the deployment's own phase/health only follow statuses of its current
//     revision — a rollout that was superseded by a newer one (queued
//     behind it on the agent) still gets its revision's outcome recorded,
//     but can't overwrite what the newer rollout reports;
//   - verifying/healthy/unhealthy are post-deployment health verification
//     results, recorded as the deployment's health (its phase stays
//     "running" — the containers are up either way);
//   - rolled_back (a rolling update restored the previous containers)
//     points the deployment back at the revision that's actually running.
func (s *Server) recordDeployStatus(ctx context.Context, deploymentID string, revision int, service, phase, message string) {
	current, err := s.store.IsCurrentRevision(ctx, deploymentID, revision)
	if err != nil {
		s.log.Error("failed to check deployment revision", "deployment_id", deploymentID, "error", err)
		current = true
	}
	if revision > 0 && message != "" && service == "" && !current {
		message = fmt.Sprintf("[revision %d] %s", revision, message)
	}

	var event store.DeploymentEvent
	switch {
	case service != "":
		event, err = s.store.AddServiceDeploymentEvent(ctx, deploymentID, service, phase, message)
	case phase == "rolled_back":
		if rerr := s.store.UpdateRevisionStatus(ctx, deploymentID, revision, "rolled_back", message); rerr != nil {
			s.log.Error("failed to update revision status", "deployment_id", deploymentID, "error", rerr)
		}
		if current {
			if rerr := s.store.RevertToPreviousRevision(ctx, deploymentID); rerr != nil {
				s.log.Error("failed to revert to previous revision", "deployment_id", deploymentID, "error", rerr)
			}
		}
		event, err = s.store.AddDeploymentEvent(ctx, deploymentID, phase, message, "")
	case phase == "verifying" || phase == "healthy" || phase == "unhealthy":
		if phase != "verifying" {
			if rerr := s.store.UpdateRevisionStatus(ctx, deploymentID, revision, phase, message); rerr != nil {
				s.log.Error("failed to update revision status", "deployment_id", deploymentID, "error", rerr)
			}
		}
		if current {
			if herr := s.store.UpdateDeploymentHealth(ctx, deploymentID, phase, message); herr != nil {
				s.log.Error("failed to update deployment health", "deployment_id", deploymentID, "error", herr)
			}
		}
		if message == "" {
			message = "health verification: " + phase
		}
		event, err = s.store.AddDeploymentEvent(ctx, deploymentID, phase, message, "")
	default:
		if phase == "running" || phase == "failed" {
			if rerr := s.store.UpdateRevisionStatus(ctx, deploymentID, revision, phase, message); rerr != nil {
				s.log.Error("failed to update revision status", "deployment_id", deploymentID, "error", rerr)
			}
		}
		if current {
			if perr := s.store.UpdateDeploymentPhase(ctx, deploymentID, phase); perr != nil {
				s.log.Error("failed to update deployment phase", "deployment_id", deploymentID, "error", perr)
			}
		}
		event, err = s.store.AddDeploymentEvent(ctx, deploymentID, phase, message, "")
	}
	if err != nil {
		s.log.Error("failed to record deployment event", "deployment_id", deploymentID, "error", err)
		return
	}
	s.events.Publish(deploymentID, event)
}

// recordBuildStatus stores a build's progress and output, then passes it on
// to whoever is following the build (the rollout waiting to deploy it, and
// any dashboards streaming its output). Stored first, so a follower that
// reloads the build on this notification sees it.
func (s *Server) recordBuildStatus(ctx context.Context, serverID string, bs *agentv1.BuildStatus) {
	status := buildPhaseToString(bs.GetPhase())
	if bs.GetLog() == "" {
		s.log.Info("build status received", "server_id", serverID, "build_id", bs.GetBuildId(), "status", status, "message", bs.GetMessage())
	}
	if err := s.store.UpdateImageBuild(ctx, bs.GetBuildId(), store.BuildUpdate{
		Status: status, Message: bs.GetMessage(), Log: bs.GetLog(), LogSeq: bs.GetLogSeq(),
		ImageID: bs.GetImageId(), Reused: bs.GetReused(),
	}); err != nil {
		s.log.Error("failed to record build status", "build_id", bs.GetBuildId(), "error", err)
	}
	s.buildBus.Publish(bs)
}

func buildPhaseToString(phase agentv1.BuildPhase) string {
	switch phase {
	case agentv1.BuildPhase_BUILD_PHASE_QUEUED:
		return store.BuildQueued
	case agentv1.BuildPhase_BUILD_PHASE_CLONING:
		return store.BuildCloning
	case agentv1.BuildPhase_BUILD_PHASE_BUILDING:
		return store.BuildBuilding
	case agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED:
		return store.BuildSucceeded
	case agentv1.BuildPhase_BUILD_PHASE_FAILED:
		return store.BuildFailed
	case agentv1.BuildPhase_BUILD_PHASE_CANCELLED:
		return store.BuildCancelled
	default:
		return ""
	}
}

func deployPhaseToString(phase agentv1.DeployPhase) string {
	switch phase {
	case agentv1.DeployPhase_DEPLOY_PHASE_PENDING:
		return "pending"
	case agentv1.DeployPhase_DEPLOY_PHASE_PULLING:
		return "pulling"
	case agentv1.DeployPhase_DEPLOY_PHASE_CREATING:
		return "creating"
	case agentv1.DeployPhase_DEPLOY_PHASE_RUNNING:
		return "running"
	case agentv1.DeployPhase_DEPLOY_PHASE_FAILED:
		return "failed"
	case agentv1.DeployPhase_DEPLOY_PHASE_VERIFYING:
		return "verifying"
	case agentv1.DeployPhase_DEPLOY_PHASE_HEALTHY:
		return "healthy"
	case agentv1.DeployPhase_DEPLOY_PHASE_UNHEALTHY:
		return "unhealthy"
	case agentv1.DeployPhase_DEPLOY_PHASE_ROLLED_BACK:
		return "rolled_back"
	default:
		return "unknown"
	}
}

func taskPhaseToString(phase agentv1.TaskPhase) string {
	switch phase {
	case agentv1.TaskPhase_TASK_PHASE_RUNNING:
		return "running"
	case agentv1.TaskPhase_TASK_PHASE_COMPLETED:
		return "completed"
	case agentv1.TaskPhase_TASK_PHASE_FAILED:
		return "failed"
	default:
		return "unknown"
	}
}

// authenticate validates the bearer credential sent as gRPC metadata on the
// Session stream and returns the server_id it belongs to.
func (s *Server) authenticate(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing credentials")
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing authorization metadata")
	}
	credential, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok || credential == "" {
		return "", status.Error(codes.Unauthenticated, "malformed authorization metadata")
	}

	sid := md.Get("x-server-id")
	if len(sid) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing x-server-id metadata")
	}

	storedHash, err := s.store.GetAgentTokenHash(ctx, sid[0])
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", status.Error(codes.Unauthenticated, "unknown server")
		}
		return "", status.Error(codes.Internal, "failed to validate credential")
	}

	if auth.HashToken(credential) != storedHash {
		return "", status.Error(codes.Unauthenticated, "invalid credential")
	}

	return sid[0], nil
}
