package docker

import (
	"testing"

	"github.com/docker/docker/api/types/volume"
)

func TestSummarizeVolume(t *testing.T) {
	t.Run("usage data present", func(t *testing.T) {
		v := volume.Volume{
			Name:       "data",
			Driver:     "local",
			Mountpoint: "/var/lib/docker/volumes/data/_data",
			Labels:     map[string]string{"env": "prod"},
			CreatedAt:  "2024-01-15T10:30:00Z",
			UsageData:  &volume.UsageData{Size: 4096, RefCount: 2},
		}
		s := summarizeVolume(v, []string{"c1", "c2"})

		if s.Name != "data" || s.Driver != "local" || s.Mountpoint != v.Mountpoint {
			t.Errorf("unexpected summary: %+v", s)
		}
		if s.SizeBytes != 4096 {
			t.Errorf("SizeBytes = %v, want 4096", s.SizeBytes)
		}
		if len(s.InUseBy) != 2 {
			t.Errorf("InUseBy = %v, want 2 entries", s.InUseBy)
		}
		if s.CreatedUnix == 0 {
			t.Error("CreatedUnix = 0, want parsed timestamp")
		}
	})

	t.Run("no usage data means 0 size, not -1", func(t *testing.T) {
		v := volume.Volume{Name: "data"}
		s := summarizeVolume(v, nil)
		if s.SizeBytes != 0 {
			t.Errorf("SizeBytes = %v, want 0 when UsageData is nil", s.SizeBytes)
		}
	})

	t.Run("negative size (driver doesn't report usage) is not carried through", func(t *testing.T) {
		v := volume.Volume{Name: "data", UsageData: &volume.UsageData{Size: -1}}
		s := summarizeVolume(v, nil)
		if s.SizeBytes != 0 {
			t.Errorf("SizeBytes = %v, want 0 when driver reports -1 (not available)", s.SizeBytes)
		}
	})

	t.Run("empty in_use_by means orphaned", func(t *testing.T) {
		v := volume.Volume{Name: "orphan"}
		s := summarizeVolume(v, nil)
		if len(s.InUseBy) != 0 {
			t.Errorf("InUseBy = %v, want empty", s.InUseBy)
		}
	})
}

func TestParseVolumeCreated(t *testing.T) {
	t.Run("empty string means unknown", func(t *testing.T) {
		if got := parseVolumeCreated(""); got != 0 {
			t.Errorf("parseVolumeCreated(\"\") = %v, want 0", got)
		}
	})

	t.Run("unparseable string means unknown", func(t *testing.T) {
		if got := parseVolumeCreated("not-a-timestamp"); got != 0 {
			t.Errorf("parseVolumeCreated(garbage) = %v, want 0", got)
		}
	})

	t.Run("valid RFC3339 timestamp parses", func(t *testing.T) {
		got := parseVolumeCreated("2024-01-15T10:30:00Z")
		if got != 1705314600 {
			t.Errorf("parseVolumeCreated = %v, want 1705314600", got)
		}
	})
}
