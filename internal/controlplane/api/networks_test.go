package api

import (
	"testing"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func TestNetworkSummaryToResponse(t *testing.T) {
	t.Run("carries every field through, attaching the owning server", func(t *testing.T) {
		n := &agentv1.NetworkSummary{
			Id:           "net123",
			Name:         "app-net",
			Driver:       "bridge",
			Scope:        "local",
			Internal:     true,
			Labels:       map[string]string{"env": "prod"},
			ContainerIds: []string{"c1", "c2"},
		}

		got := networkSummaryToResponse("srv1", "prod-1", n)

		if got.ServerID != "srv1" || got.ServerName != "prod-1" {
			t.Errorf("server fields = %q/%q, want srv1/prod-1", got.ServerID, got.ServerName)
		}
		if got.ID != "net123" || got.Name != "app-net" || got.Driver != "bridge" || got.Scope != "local" {
			t.Errorf("unexpected response: %+v", got)
		}
		if !got.Internal {
			t.Error("Internal = false, want true")
		}
		if len(got.ContainerIDs) != 2 {
			t.Errorf("ContainerIDs = %v, want 2 entries", got.ContainerIDs)
		}
	})

	t.Run("zero-value network summary doesn't panic and defaults sensibly", func(t *testing.T) {
		got := networkSummaryToResponse("srv1", "prod-1", &agentv1.NetworkSummary{})
		if got.Internal {
			t.Error("Internal = true, want false for zero-value input")
		}
		if len(got.ContainerIDs) != 0 {
			t.Errorf("ContainerIDs = %v, want empty", got.ContainerIDs)
		}
	})
}
