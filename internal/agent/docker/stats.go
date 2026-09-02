package docker

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// ContainerStats is one point-in-time resource-usage sample for a running
// container. The Engine API doesn't report percentages directly — they're
// derived from cumulative cgroup counters the same way the `docker stats`
// CLI computes them (see calculateCPUPercent/calculateMemUsage below).
type ContainerStats struct {
	CPUPercent      float64
	MemUsageBytes   int64
	MemLimitBytes   int64
	MemPercent      float64
	NetRxBytes      int64
	NetTxBytes      int64
	BlockReadBytes  int64
	BlockWriteBytes int64
	PIDs            int64
}

// CollectContainerStats takes a single stats snapshot for containerID, the
// same shape `docker stats --no-stream` reports. stream=false makes the
// daemon itself wait for a second sample internally so CPUStats/PreCPUStats
// carry a real delta, rather than this call needing to poll twice.
func CollectContainerStats(ctx context.Context, cli *client.Client, containerID string) (ContainerStats, error) {
	resp, err := cli.ContainerStats(ctx, containerID, false)
	if err != nil {
		return ContainerStats{}, err
	}
	defer resp.Body.Close()

	var raw container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return ContainerStats{}, err
	}

	memUsage, memLimit := memoryUsageAndLimit(&raw)
	memPercent := 0.0
	if memLimit > 0 {
		memPercent = memUsage / memLimit * 100
	}
	rx, tx := networkTotals(&raw)
	read, write := blockIOTotals(&raw)

	return ContainerStats{
		CPUPercent:      cpuPercent(&raw),
		MemUsageBytes:   int64(memUsage),
		MemLimitBytes:   int64(memLimit),
		MemPercent:      memPercent,
		NetRxBytes:      rx,
		NetTxBytes:      tx,
		BlockReadBytes:  read,
		BlockWriteBytes: write,
		PIDs:            int64(raw.PidsStats.Current),
	}, nil
}

// cpuPercent mirrors the Docker CLI's calculateCPUPercentUnix: CPU time
// consumed since the previous sample, as a percentage of one core, scaled
// by the number of cores available to the container.
func cpuPercent(v *container.StatsResponse) float64 {
	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage) - float64(v.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(v.CPUStats.SystemUsage) - float64(v.PreCPUStats.SystemUsage)
	if systemDelta <= 0 || cpuDelta <= 0 {
		return 0
	}
	onlineCPUs := float64(v.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
	}
	if onlineCPUs == 0 {
		onlineCPUs = 1
	}
	return (cpuDelta / systemDelta) * onlineCPUs * 100
}

// memoryUsageAndLimit excludes page cache from usage (same as `docker
// stats`) — cgroup v1 exposes it as "total_inactive_file", cgroup v2 as
// "inactive_file" — so usage reflects what's actually pinned by the
// workload, not reclaimable cache.
func memoryUsageAndLimit(v *container.StatsResponse) (usage, limit float64) {
	usage = float64(v.MemoryStats.Usage)
	if cache, ok := v.MemoryStats.Stats["total_inactive_file"]; ok && float64(cache) < usage {
		usage -= float64(cache)
	} else if cache, ok := v.MemoryStats.Stats["inactive_file"]; ok && float64(cache) < usage {
		usage -= float64(cache)
	}
	return usage, float64(v.MemoryStats.Limit)
}

func networkTotals(v *container.StatsResponse) (rx, tx int64) {
	for _, n := range v.Networks {
		rx += int64(n.RxBytes)
		tx += int64(n.TxBytes)
	}
	return rx, tx
}

func blockIOTotals(v *container.StatsResponse) (read, write int64) {
	for _, entry := range v.BlkioStats.IoServiceBytesRecursive {
		switch {
		case strings.EqualFold(entry.Op, "read"):
			read += int64(entry.Value)
		case strings.EqualFold(entry.Op, "write"):
			write += int64(entry.Value)
		}
	}
	return read, write
}
