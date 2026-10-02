package api

import (
	"testing"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func TestContainerConfigFromStateDedupesPortFamilies(t *testing.T) {
	state := &store.ContainerState{
		Name: "web",
		Ports: []store.ContainerPort{
			{IP: "0.0.0.0", PrivatePort: 80, PublicPort: 18080, Type: "tcp"},
			{IP: "::", PrivatePort: 80, PublicPort: 18080, Type: "tcp"},
			{PrivatePort: 443, Type: "tcp"},
		},
	}
	cfg := containerConfigFromState(state, &agentv1.ContainerDetail{}, "nginx:alpine")
	if len(cfg.GetPorts()) != 1 || cfg.GetPorts()[0].GetHostPort() != 18080 {
		t.Fatalf("ports = %v, want one 80->18080 binding", cfg.GetPorts())
	}
}
