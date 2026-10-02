package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestCPUPercent(t *testing.T) {
	tests := []struct {
		name string
		v    container.StatsResponse
		want float64
	}{
		{
			name: "half a core busy on a 4-core host",
			v: container.StatsResponse{
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 2000},
					SystemUsage: 10000,
					OnlineCPUs:  4,
				},
				PreCPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 1000},
					SystemUsage: 8000,
				},
			},
			// cpuDelta=1000, systemDelta=2000 -> (1000/2000)*4*100 = 200%
			want: 200,
		},
		{
			name: "no system delta yields 0, not a div-by-zero panic",
			v: container.StatsResponse{
				CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 2000}, SystemUsage: 5000, OnlineCPUs: 2},
				PreCPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 2000}, SystemUsage: 5000},
			},
			want: 0,
		},
		{
			name: "negative delta (counter reset) yields 0",
			v: container.StatsResponse{
				CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 100}, SystemUsage: 5000, OnlineCPUs: 2},
				PreCPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 2000}, SystemUsage: 4000},
			},
			want: 0,
		},
		{
			name: "falls back to len(PercpuUsage) when OnlineCPUs is unset",
			v: container.StatsResponse{
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 2000, PercpuUsage: []uint64{1, 2, 3}},
					SystemUsage: 10000,
				},
				PreCPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 1000},
					SystemUsage: 8000,
				},
			},
			// cpuDelta=1000, systemDelta=2000 -> (1000/2000)*3*100 = 150%
			want: 150,
		},
		{
			name: "falls back to 1 core when neither OnlineCPUs nor PercpuUsage is set",
			v: container.StatsResponse{
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 2000},
					SystemUsage: 10000,
				},
				PreCPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 1000},
					SystemUsage: 8000,
				},
			},
			// cpuDelta=1000, systemDelta=2000 -> (1000/2000)*1*100 = 50%
			want: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cpuPercent(&tt.v); got != tt.want {
				t.Errorf("cpuPercent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMemoryUsageAndLimit(t *testing.T) {
	tests := []struct {
		name      string
		v         container.StatsResponse
		wantUsage float64
		wantLimit float64
	}{
		{
			name: "cgroup v1 excludes total_inactive_file cache from usage",
			v: container.StatsResponse{
				MemoryStats: container.MemoryStats{
					Usage: 1000,
					Limit: 5000,
					Stats: map[string]uint64{"total_inactive_file": 200},
				},
			},
			wantUsage: 800,
			wantLimit: 5000,
		},
		{
			name: "cgroup v2 excludes inactive_file cache from usage",
			v: container.StatsResponse{
				MemoryStats: container.MemoryStats{
					Usage: 1000,
					Limit: 5000,
					Stats: map[string]uint64{"inactive_file": 300},
				},
			},
			wantUsage: 700,
			wantLimit: 5000,
		},
		{
			name: "no cache stats leaves usage untouched",
			v: container.StatsResponse{
				MemoryStats: container.MemoryStats{Usage: 1000, Limit: 5000},
			},
			wantUsage: 1000,
			wantLimit: 5000,
		},
		{
			name: "cache larger than usage is ignored rather than going negative",
			v: container.StatsResponse{
				MemoryStats: container.MemoryStats{
					Usage: 1000,
					Limit: 5000,
					Stats: map[string]uint64{"total_inactive_file": 5000},
				},
			},
			wantUsage: 1000,
			wantLimit: 5000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUsage, gotLimit := memoryUsageAndLimit(&tt.v)
			if gotUsage != tt.wantUsage {
				t.Errorf("usage = %v, want %v", gotUsage, tt.wantUsage)
			}
			if gotLimit != tt.wantLimit {
				t.Errorf("limit = %v, want %v", gotLimit, tt.wantLimit)
			}
		})
	}
}

func TestNetworkTotals(t *testing.T) {
	v := container.StatsResponse{
		Networks: map[string]container.NetworkStats{
			"eth0": {RxBytes: 100, TxBytes: 50},
			"eth1": {RxBytes: 30, TxBytes: 20},
		},
	}
	rx, tx := networkTotals(&v)
	if rx != 130 {
		t.Errorf("rx = %v, want 130", rx)
	}
	if tx != 70 {
		t.Errorf("tx = %v, want 70", tx)
	}
}

func TestNetworkTotalsEmpty(t *testing.T) {
	rx, tx := networkTotals(&container.StatsResponse{})
	if rx != 0 || tx != 0 {
		t.Errorf("rx,tx = %v,%v, want 0,0", rx, tx)
	}
}

func TestBlockIOTotals(t *testing.T) {
	v := container.StatsResponse{
		BlkioStats: container.BlkioStats{
			IoServiceBytesRecursive: []container.BlkioStatEntry{
				{Op: "Read", Value: 100},
				{Op: "Write", Value: 40},
				{Op: "read", Value: 5},    // case-insensitive op matching
				{Op: "Total", Value: 999}, // ignored — neither read nor write
			},
		},
	}
	read, write := blockIOTotals(&v)
	if read != 105 {
		t.Errorf("read = %v, want 105", read)
	}
	if write != 40 {
		t.Errorf("write = %v, want 40", write)
	}
}
