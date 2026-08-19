// Package grpcserver implements the control-plane side of the AgentSession
// gRPC service: agent enrollment and the persistent heartbeat/command stream.
package grpcserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// Server implements agentv1.AgentSessionServer.
//
// M1 scope: no token validation and no persistence — it exists to prove the
// wire protocol. Token validation against enrollment_tokens and writing to
// the servers table land in M2/M3.
type Server struct {
	agentv1.UnimplementedAgentSessionServer

	log *slog.Logger
}

func New(log *slog.Logger) *Server {
	return &Server{log: log}
}

func (s *Server) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	if req.GetToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	serverID, err := randomID()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate server id")
	}
	credential, err := randomID()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate agent credential")
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
			s.log.Info("heartbeat received",
				"server_id", hb.GetServerId(),
				"cpu_percent", hb.GetResources().GetCpuPercent(),
				"mem_percent", hb.GetResources().GetMemPercent(),
				"disk_percent", hb.GetResources().GetDiskPercent(),
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

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
