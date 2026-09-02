package docker

import (
	"context"
	"time"

	"github.com/docker/docker/client"
)

// HealthCheckEntry is one entry from Docker's bounded health-check result
// history (State.Health.Log, capped by the Engine itself at its last 5
// results) — kept for the troubleshooting view's health-check history,
// discarded by earlier versions of InspectContainer which only kept the
// current status.
type HealthCheckEntry struct {
	Start    time.Time
	End      time.Time
	ExitCode int
	Output   string
}

// ContainerDetail carries the fields that require a ContainerInspect call —
// too expensive to collect for every container on every heartbeat across a
// fleet, so InspectContainer is only called on demand.
type ContainerDetail struct {
	Found                      bool
	ErrorMessage               string
	Env                        []string
	RestartPolicyName          string
	RestartPolicyMaxRetryCount int
	HealthStatus               string
	HealthFailingStreak        int
	RestartCount               int
	HealthLog                  []HealthCheckEntry
	Command                    []string
	Entrypoint                 []string
	WorkingDir                 string
	Labels                     map[string]string
	Image                      string
	NanoCPUs                   int64
	MemoryLimitBytes           int64
	MemoryReservationBytes     int64
	PidsLimit                  int64
}

// InspectContainer runs a single ContainerInspect call for containerID.
func InspectContainer(ctx context.Context, cli *client.Client, containerID string) ContainerDetail {
	info, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return ContainerDetail{Found: false, ErrorMessage: err.Error()}
	}

	detail := ContainerDetail{Found: true}
	if info.ContainerJSONBase != nil {
		detail.RestartCount = info.RestartCount
		if info.State != nil && info.State.Health != nil {
			detail.HealthStatus = string(info.State.Health.Status)
			detail.HealthFailingStreak = info.State.Health.FailingStreak
			for _, h := range info.State.Health.Log {
				if h == nil {
					continue
				}
				detail.HealthLog = append(detail.HealthLog, HealthCheckEntry{
					Start:    h.Start,
					End:      h.End,
					ExitCode: h.ExitCode,
					Output:   h.Output,
				})
			}
		}
		if info.HostConfig != nil {
			detail.RestartPolicyName = string(info.HostConfig.RestartPolicy.Name)
			detail.RestartPolicyMaxRetryCount = info.HostConfig.RestartPolicy.MaximumRetryCount
			detail.NanoCPUs = info.HostConfig.NanoCPUs
			detail.MemoryLimitBytes = info.HostConfig.Memory
			detail.MemoryReservationBytes = info.HostConfig.MemoryReservation
			if info.HostConfig.PidsLimit != nil {
				detail.PidsLimit = *info.HostConfig.PidsLimit
			}
		}
	}
	if info.Config != nil {
		detail.Env = info.Config.Env
		detail.Command = info.Config.Cmd
		detail.Entrypoint = info.Config.Entrypoint
		detail.WorkingDir = info.Config.WorkingDir
		detail.Labels = info.Config.Labels
		detail.Image = info.Config.Image
	}
	return detail
}
