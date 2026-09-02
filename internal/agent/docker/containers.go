package docker

import (
	"context"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// ContainerPort is one published or exposed port on a container.
type ContainerPort struct {
	IP          string
	PrivatePort uint16
	PublicPort  uint16
	Type        string
}

// ContainerNetwork is one Docker network a container is attached to.
type ContainerNetwork struct {
	Name      string
	IPAddress string
}

// ContainerMount is one volume or bind mount attached to a container.
type ContainerMount struct {
	Type        string
	Name        string
	Source      string
	Destination string
	ReadWrite   bool
}

// ContainerPortSpec is one port mapping requested for a standalone
// (non-stack) container.
type ContainerPortSpec struct {
	ContainerPort uint16
	HostPort      uint16
	Protocol      string
}

// ContainerVolumeSpec is one named-volume mount requested for a standalone
// container. Bind mounts are deliberately unsupported here too — see
// ensureVolumes' doc comment in deploy.go on why.
type ContainerVolumeSpec struct {
	VolumeName string
	Target     string
	ReadOnly   bool
}

// ContainerConfig is the full desired shape of a standalone container (one
// created or recreated directly through container management, not part of
// a deployed stack). Mirrors agentv1.ContainerConfig, kept as a separate
// type so the docker package doesn't depend on agentv1 — same separation
// ContainerDetail already follows for InspectContainer.
type ContainerConfig struct {
	Image                      string
	Name                       string
	Command                    []string
	Env                        []string
	Ports                      []ContainerPortSpec
	Volumes                    []ContainerVolumeSpec
	RestartPolicyName          string
	RestartPolicyMaxRetryCount int
	Labels                     map[string]string
	// Resource limits — 0 means "not set" (unlimited), same convention as
	// RestartPolicyMaxRetryCount/ContainerPortSpec.HostPort above.
	NanoCPUs               int64
	MemoryLimitBytes       int64
	MemoryReservationBytes int64
	PidsLimit              int64
}

// ContainerSummary is a snapshot of one container's identity and state, for
// reporting on the agent's heartbeat. Every field here comes from the same
// ContainerList call — no extra Docker API round-trip — so it's cheap
// enough to refresh on every heartbeat tick. Fields that require a
// ContainerInspect call (env vars, restart policy, structured health) are
// fetched separately and only on demand — see InspectContainer.
type ContainerSummary struct {
	ID           string
	Name         string
	State        string
	DeploymentID string // empty if not managed by this agent (no deployment label)
	Image        string
	ImageID      string
	CreatedUnix  int64
	Status       string
	Ports        []ContainerPort
	Networks     []ContainerNetwork
	Mounts       []ContainerMount
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

		ports := make([]ContainerPort, 0, len(c.Ports))
		for _, p := range c.Ports {
			ports = append(ports, ContainerPort{
				IP:          p.IP,
				PrivatePort: p.PrivatePort,
				PublicPort:  p.PublicPort,
				Type:        p.Type,
			})
		}

		var networks []ContainerNetwork
		if c.NetworkSettings != nil {
			networks = make([]ContainerNetwork, 0, len(c.NetworkSettings.Networks))
			for name, ep := range c.NetworkSettings.Networks {
				ipAddress := ""
				if ep != nil {
					ipAddress = ep.IPAddress
				}
				networks = append(networks, ContainerNetwork{Name: name, IPAddress: ipAddress})
			}
		}

		mounts := make([]ContainerMount, 0, len(c.Mounts))
		for _, m := range c.Mounts {
			mounts = append(mounts, ContainerMount{
				Type:        string(m.Type),
				Name:        m.Name,
				Source:      m.Source,
				Destination: m.Destination,
				ReadWrite:   m.RW,
			})
		}

		summaries = append(summaries, ContainerSummary{
			ID:           c.ID,
			Name:         name,
			State:        c.State,
			DeploymentID: c.Labels[labelDeploymentID],
			Image:        c.Image,
			ImageID:      c.ImageID,
			CreatedUnix:  c.Created,
			Status:       c.Status,
			Ports:        ports,
			Networks:     networks,
			Mounts:       mounts,
		})
	}
	return summaries, nil
}
