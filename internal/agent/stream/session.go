// Package stream manages the agent's persistent connection to the control
// plane: enrolling once, then keeping the bidirectional Session stream open
// with reconnect/backoff and periodic heartbeats.
package stream

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/enroll"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

const (
	heartbeatInterval = 20 * time.Second
	minBackoff        = 1 * time.Second
	maxBackoff        = 60 * time.Second
)

// Runner owns the connection lifecycle: dial, enroll (once), then keep the
// Session stream alive, reconnecting with backoff on failure.
type Runner struct {
	Addr         string
	Token        string
	Hostname     string
	OS           string
	Arch         string
	AgentVersion string

	log *slog.Logger
}

func New(log *slog.Logger, addr, token, hostname, osName, arch, agentVersion string) *Runner {
	return &Runner{
		Addr:         addr,
		Token:        token,
		Hostname:     hostname,
		OS:           osName,
		Arch:         arch,
		AgentVersion: agentVersion,
		log:          log,
	}
}

// Run blocks, enrolling once and then looping the Session stream with
// reconnect/backoff until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	conn, err := grpc.NewClient(r.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := agentv1.NewAgentSessionClient(conn)

	identity, err := enroll.Enroll(ctx, client, r.Token, r.Hostname, r.OS, r.Arch, r.AgentVersion)
	if err != nil {
		return err
	}
	r.log.Info("enrolled with control plane", "server_id", identity.ServerID)

	backoff := minBackoff
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := r.runSession(ctx, client, identity); err != nil {
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

func (r *Runner) runSession(ctx context.Context, client agentv1.AgentSessionClient, identity *enroll.Identity) error {
	stream, err := client.Session(ctx)
	if err != nil {
		return err
	}

	// Reset backoff implicitly by returning nil once connected; the caller's
	// loop only backs off after a failed/ended session.
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	// Send an immediate heartbeat on connect rather than waiting a full
	// interval, so a fresh/reconnected agent shows up promptly.
	if err := sendHeartbeat(stream, identity.ServerID); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := sendHeartbeat(stream, identity.ServerID); err != nil {
				return err
			}
		}
	}
}

func sendHeartbeat(stream agentv1.AgentSession_SessionClient, serverID string) error {
	return stream.Send(&agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_Heartbeat{
			Heartbeat: &agentv1.Heartbeat{
				ServerId: serverID,
				SentAt:   timestamppb.Now(),
				Resources: &agentv1.ResourceSnapshot{
					CpuPercent:  0,
					MemPercent:  0,
					DiskPercent: 0,
				},
			},
		},
	})
}

func jitter(d time.Duration) time.Duration {
	//nolint:gosec // non-cryptographic jitter is fine here
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}
