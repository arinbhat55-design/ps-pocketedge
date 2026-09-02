package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestResourcesFromConfig(t *testing.T) {
	t.Run("zero fields mean unlimited", func(t *testing.T) {
		res := resourcesFromConfig(ContainerConfig{})
		if res.NanoCPUs != 0 {
			t.Errorf("NanoCPUs = %v, want 0", res.NanoCPUs)
		}
		if res.Memory != 0 {
			t.Errorf("Memory = %v, want 0", res.Memory)
		}
		if res.MemoryReservation != 0 {
			t.Errorf("MemoryReservation = %v, want 0", res.MemoryReservation)
		}
		if res.PidsLimit != nil {
			t.Errorf("PidsLimit = %v, want nil (not set)", *res.PidsLimit)
		}
	})

	t.Run("non-zero fields carry through, PidsLimit as a pointer", func(t *testing.T) {
		res := resourcesFromConfig(ContainerConfig{
			NanoCPUs:               1500000000,
			MemoryLimitBytes:       134217728,
			MemoryReservationBytes: 67108864,
			PidsLimit:              100,
		})
		if res.NanoCPUs != 1500000000 {
			t.Errorf("NanoCPUs = %v, want 1500000000", res.NanoCPUs)
		}
		if res.Memory != 134217728 {
			t.Errorf("Memory = %v, want 134217728", res.Memory)
		}
		if res.MemoryReservation != 67108864 {
			t.Errorf("MemoryReservation = %v, want 67108864", res.MemoryReservation)
		}
		if res.PidsLimit == nil || *res.PidsLimit != 100 {
			t.Errorf("PidsLimit = %v, want pointer to 100", res.PidsLimit)
		}
	})
}

func TestConfigFromInspectRoundTripsResourceLimits(t *testing.T) {
	pidsLimit := int64(50)
	info := container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			HostConfig: &container.HostConfig{
				Resources: container.Resources{
					NanoCPUs:          250000000,
					Memory:            67108864,
					MemoryReservation: 33554432,
					PidsLimit:         &pidsLimit,
				},
			},
		},
		Config: &container.Config{},
	}

	cfg := configFromInspect(info, "clone-name")

	if cfg.NanoCPUs != 250000000 {
		t.Errorf("NanoCPUs = %v, want 250000000", cfg.NanoCPUs)
	}
	if cfg.MemoryLimitBytes != 67108864 {
		t.Errorf("MemoryLimitBytes = %v, want 67108864", cfg.MemoryLimitBytes)
	}
	if cfg.MemoryReservationBytes != 33554432 {
		t.Errorf("MemoryReservationBytes = %v, want 33554432", cfg.MemoryReservationBytes)
	}
	if cfg.PidsLimit != 50 {
		t.Errorf("PidsLimit = %v, want 50", cfg.PidsLimit)
	}
}

func TestConfigFromInspectNilPidsLimitStaysZero(t *testing.T) {
	info := container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			HostConfig: &container.HostConfig{},
		},
		Config: &container.Config{},
	}

	cfg := configFromInspect(info, "clone-name")

	if cfg.PidsLimit != 0 {
		t.Errorf("PidsLimit = %v, want 0 when HostConfig.PidsLimit is nil", cfg.PidsLimit)
	}
}
