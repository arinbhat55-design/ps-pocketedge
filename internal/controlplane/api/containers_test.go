package api

import "testing"

func TestContainerConfigRequestToProtoCarriesResourceLimits(t *testing.T) {
	req := containerConfigRequest{
		Image:                  "nginx:latest",
		Name:                   "web",
		NanoCPUs:               1500000000,
		MemoryLimitBytes:       134217728,
		MemoryReservationBytes: 67108864,
		PidsLimit:              100,
	}

	cfg := req.toProto()

	if cfg.GetNanoCpus() != 1500000000 {
		t.Errorf("NanoCpus = %v, want 1500000000", cfg.GetNanoCpus())
	}
	if cfg.GetMemoryLimitBytes() != 134217728 {
		t.Errorf("MemoryLimitBytes = %v, want 134217728", cfg.GetMemoryLimitBytes())
	}
	if cfg.GetMemoryReservationBytes() != 67108864 {
		t.Errorf("MemoryReservationBytes = %v, want 67108864", cfg.GetMemoryReservationBytes())
	}
	if cfg.GetPidsLimit() != 100 {
		t.Errorf("PidsLimit = %v, want 100", cfg.GetPidsLimit())
	}
}

func TestContainerConfigRequestToProtoZeroLimitsMeanUnset(t *testing.T) {
	req := containerConfigRequest{Image: "nginx:latest", Name: "web"}

	cfg := req.toProto()

	if cfg.GetNanoCpus() != 0 || cfg.GetMemoryLimitBytes() != 0 ||
		cfg.GetMemoryReservationBytes() != 0 || cfg.GetPidsLimit() != 0 {
		t.Errorf("expected all resource limits to default to 0 (unset), got %+v", cfg)
	}
}
