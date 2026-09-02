package api

import (
	"testing"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func TestVolumeSummaryToResponse(t *testing.T) {
	t.Run("non-empty InUseBy means not orphaned", func(t *testing.T) {
		v := &agentv1.VolumeSummary{
			Name:        "data",
			Driver:      "local",
			Mountpoint:  "/var/lib/docker/volumes/data/_data",
			SizeBytes:   4096,
			InUseBy:     []string{"c1"},
			CreatedUnix: 1700000000,
		}

		got := volumeSummaryToResponse("srv1", "prod-1", v)

		if got.Orphaned {
			t.Error("Orphaned = true, want false when InUseBy is non-empty")
		}
		if got.ServerID != "srv1" || got.ServerName != "prod-1" {
			t.Errorf("server fields = %q/%q, want srv1/prod-1", got.ServerID, got.ServerName)
		}
		if got.Name != "data" || got.SizeBytes != 4096 {
			t.Errorf("unexpected response: %+v", got)
		}
	})

	t.Run("empty InUseBy means orphaned", func(t *testing.T) {
		v := &agentv1.VolumeSummary{Name: "orphan"}
		got := volumeSummaryToResponse("srv1", "prod-1", v)
		if !got.Orphaned {
			t.Error("Orphaned = false, want true when InUseBy is empty")
		}
	})

	t.Run("nil VolumeSummary (e.g. a malformed reply) doesn't panic", func(t *testing.T) {
		got := volumeSummaryToResponse("srv1", "prod-1", &agentv1.VolumeSummary{})
		if !got.Orphaned {
			t.Error("Orphaned = false, want true for a zero-value volume")
		}
	})
}
