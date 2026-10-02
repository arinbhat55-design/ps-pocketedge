package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestResourceUpdate(t *testing.T) {
	pids := int64(100)

	res, err := resourceUpdate(container.Resources{}, 500_000_000, 256<<20, 0, 100)
	if err != nil {
		t.Fatalf("setting limits on an unlimited container: %v", err)
	}
	if res.Memory != 256<<20 || res.MemorySwap != 512<<20 {
		t.Errorf("memory/swap = %d/%d, want 256MiB/512MiB (swap must be sent with memory)", res.Memory, res.MemorySwap)
	}
	if res.PidsLimit == nil || *res.PidsLimit != 100 {
		t.Errorf("pids limit = %v, want 100", res.PidsLimit)
	}

	res, err = resourceUpdate(container.Resources{Memory: 256 << 20, MemorySwap: -1}, 0, 1<<30, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.MemorySwap != -1 {
		t.Errorf("unlimited swap changed to %d", res.MemorySwap)
	}

	res, err = resourceUpdate(container.Resources{PidsLimit: &pids}, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.PidsLimit == nil || *res.PidsLimit != -1 {
		t.Errorf("clearing the pids limit sent %v, want -1", res.PidsLimit)
	}

	for name, current := range map[string]container.Resources{
		"cpu":    {NanoCPUs: 500_000_000},
		"memory": {Memory: 256 << 20},
	} {
		if _, err := resourceUpdate(current, 0, 0, 0, 0); err == nil {
			t.Errorf("clearing the %s limit: got nil error, want one explaining Docker can't", name)
		}
	}
}
