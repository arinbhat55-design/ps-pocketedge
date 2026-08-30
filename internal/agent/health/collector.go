// Package health collects host resource metrics for the agent's heartbeat.
package health

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// diskPath is the mount point sampled for disk usage. Root-only for this
// MVP slice — not configurable per-volume yet.
const diskPath = "/"

// Snapshot is a single host resource sample.
type Snapshot struct {
	CPUPercent  float64
	MemPercent  float64
	DiskPercent float64
}

// Collect samples current host CPU, memory, and disk usage. Each metric is
// sampled independently: a failure on one (e.g. an unusual disk layout on a
// Pi image) doesn't prevent the others from being reported, since a
// heartbeat must still go out on schedule.
func Collect(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	var errs []error

	if pct, err := cpu.PercentWithContext(ctx, 0, false); err != nil {
		errs = append(errs, fmt.Errorf("cpu: %w", err))
	} else if len(pct) > 0 {
		snap.CPUPercent = pct[0]
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		errs = append(errs, fmt.Errorf("mem: %w", err))
	} else {
		snap.MemPercent = vm.UsedPercent
	}

	if du, err := disk.UsageWithContext(ctx, diskPath); err != nil {
		errs = append(errs, fmt.Errorf("disk: %w", err))
	} else {
		snap.DiskPercent = du.UsedPercent
	}

	if len(errs) > 0 {
		return snap, fmt.Errorf("health collection: %w", errs[0])
	}
	return snap, nil
}

// TotalMemory returns total host memory in bytes, for classifying a
// device's tier (see edge-device tuning's adaptive heartbeat interval) —
// the one hardware signal simple enough to distinguish a Pi from a cloud
// VM without adding a whole capability-negotiation scheme.
func TotalMemory(ctx context.Context) (uint64, error) {
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return 0, err
	}
	return vm.Total, nil
}
