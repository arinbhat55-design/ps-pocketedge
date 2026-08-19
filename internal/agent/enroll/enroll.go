// Package enroll implements the agent's one-shot enrollment call, exchanging
// a one-time token for a long-lived credential.
package enroll

import (
	"context"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// Identity is what the agent persists locally after a successful enrollment.
type Identity struct {
	ServerID   string
	Credential string
}

// Enroll exchanges token for a long-lived Identity via a unary gRPC call.
func Enroll(ctx context.Context, client agentv1.AgentSessionClient, token, hostname, os, arch, agentVersion string) (*Identity, error) {
	resp, err := client.Enroll(ctx, &agentv1.EnrollRequest{
		Token:        token,
		Hostname:     hostname,
		Os:           os,
		Arch:         arch,
		AgentVersion: agentVersion,
	})
	if err != nil {
		return nil, err
	}

	return &Identity{
		ServerID:   resp.GetServerId(),
		Credential: resp.GetAgentCredential(),
	}, nil
}
