// Package grpcserver implements the control-plane side of the AgentSession
// gRPC service: agent enrollment and the persistent heartbeat/command stream.
package grpcserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// Server implements agentv1.AgentSessionServer.
//
// M2 scope: enrollment and heartbeats are persisted to Postgres. Token
// validation against enrollment_tokens (rather than accepting any token)
// lands in M3.
type Server struct {
	agentv1.UnimplementedAgentSessionServer

	log   *slog.Logger
	store *store.Store
}

func New(log *slog.Logger, st *store.Store) *Server {
	return &Server{log: log, store: st}
}

func (s *Server) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	if req.GetToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	credential, err := randomToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate agent credential")
	}

	serverID, err := s.store.CreateServer(ctx, req.GetHostname(), req.GetOs(), req.GetArch(), req.GetAgentVersion(), hashToken(credential))
	if err != nil {
		s.log.Error("failed to persist enrolled server", "error", err)
		return nil, status.Error(codes.Internal, "failed to persist server")
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

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msg, err := stream.Recv()
		if err != nil {
			s.log.Info("agent session closed", "error", err)
			return err
		}

		switch payload := msg.Payload.(type) {
		case *agentv1.AgentMessage_Heartbeat:
			hb := payload.Heartbeat
			resources := store.ResourceSnapshot{
				CPUPercent:  hb.GetResources().GetCpuPercent(),
				MemPercent:  hb.GetResources().GetMemPercent(),
				DiskPercent: hb.GetResources().GetDiskPercent(),
			}
			if err := s.store.RecordHeartbeat(ctx, hb.GetServerId(), resources); err != nil {
				s.log.Error("failed to record heartbeat", "server_id", hb.GetServerId(), "error", err)
				continue
			}
			s.log.Info("heartbeat recorded",
				"server_id", hb.GetServerId(),
				"cpu_percent", resources.CPUPercent,
				"mem_percent", resources.MemPercent,
				"disk_percent", resources.DiskPercent,
				"containers", len(hb.GetContainers()),
			)
		case *agentv1.AgentMessage_DeployStatus:
			ds := payload.DeployStatus
			s.log.Info("deploy status received",
				"deployment_id", ds.GetDeploymentId(),
				"phase", ds.GetPhase(),
				"message", ds.GetMessage(),
			)
		default:
			s.log.Warn("unknown agent message payload")
		}
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
