package docker

import (
	"context"

	"github.com/docker/docker/client"
)

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
		}
		if info.HostConfig != nil {
			detail.RestartPolicyName = string(info.HostConfig.RestartPolicy.Name)
			detail.RestartPolicyMaxRetryCount = info.HostConfig.RestartPolicy.MaximumRetryCount
		}
	}
	if info.Config != nil {
		detail.Env = info.Config.Env
	}
	return detail
}
