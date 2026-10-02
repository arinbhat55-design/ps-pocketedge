// Package health collects host resource metrics for the agent's heartbeat.
package health

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

// diskPath is the mount point sampled for disk usage. Root-only for this
// MVP slice — not configurable per-volume yet.
const diskPath = "/"

// Snapshot is a single host resource sample.
type Snapshot struct {
	CPUPercent  float64
	MemPercent  float64
	DiskPercent float64
	// TotalMemoryBytes/NumCPUs/TotalDiskBytes are host capacity, not
	// usage — 0 means that particular collection failed (see Collect's
	// doc comment), not "zero capacity".
	TotalMemoryBytes uint64
	NumCPUs          uint32
	TotalDiskBytes   uint64
	UsedMemoryBytes  *uint64
	UsedDiskBytes    *uint64
}

// Collect samples current host CPU, memory, and disk usage, plus each
// one's total capacity (for the control plane's "does this deployment fit"
// pre-deployment check). Each metric is sampled independently: a failure
// on one (e.g. an unusual disk layout on a Pi image) doesn't prevent the
// others from being reported, since a heartbeat must still go out on
// schedule — that metric's fields are just left at their zero value.
func Collect(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	var errs []error

	if pct, err := cpu.PercentWithContext(ctx, 0, false); err != nil {
		errs = append(errs, fmt.Errorf("cpu: %w", err))
	} else if len(pct) > 0 {
		snap.CPUPercent = pct[0]
	}
	if n, err := cpu.CountsWithContext(ctx, true); err != nil {
		errs = append(errs, fmt.Errorf("cpu count: %w", err))
	} else {
		snap.NumCPUs = uint32(n)
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		errs = append(errs, fmt.Errorf("mem: %w", err))
	} else {
		snap.MemPercent = vm.UsedPercent
		snap.TotalMemoryBytes = vm.Total
		snap.UsedMemoryBytes = &vm.Used
	}

	if du, err := disk.UsageWithContext(ctx, diskPath); err != nil {
		errs = append(errs, fmt.Errorf("disk: %w", err))
	} else {
		snap.DiskPercent = du.UsedPercent
		snap.TotalDiskBytes = du.Total
		snap.UsedDiskBytes = &du.Used
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
