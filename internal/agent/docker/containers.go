package docker

import (
	"context"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// ContainerSummary is a snapshot of one container's identity and state, for
// reporting on the agent's heartbeat.
type ContainerSummary struct {
	ID           string
	Name         string
	State        string
	DeploymentID string // empty if not managed by this agent (no deployment label)
}

// ListContainers returns every container on the host (including ones not
// created by this agent), for the heartbeat's live inventory. DeploymentID
// is populated from labelDeploymentID when present.
func ListContainers(ctx context.Context, cli *client.Client) ([]ContainerSummary, error) {
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	summaries := make([]ContainerSummary, 0, len(containers))
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			// Docker prepends a leading '/' to container names in
			// ContainerList responses.
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		summaries = append(summaries, ContainerSummary{
			ID:           c.ID,
			Name:         name,
			State:        c.State,
			DeploymentID: c.Labels[labelDeploymentID],
		})
	}
	return summaries, nil
}
