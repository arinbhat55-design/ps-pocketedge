package docker

import (
	"context"
	"errors"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// NetworkSummary is a snapshot of one Docker network, from the same
// NetworkList call `docker network ls` uses. ContainerIDs is the set of
// containers currently attached, used both for display and to guard
// removing a network that's still in use.
type NetworkSummary struct {
	ID           string
	Name         string
	Driver       string
	Scope        string
	Internal     bool
	Labels       map[string]string
	ContainerIDs []string
}

// ListNetworks returns every network present on the host, for the on-demand
// Networks tab (deliberately not part of the heartbeat, same reasoning as
// ListImages).
func ListNetworks(ctx context.Context, cli *client.Client) ([]NetworkSummary, error) {
	networks, err := cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, err
	}

	summaries := make([]NetworkSummary, 0, len(networks))
	for _, n := range networks {
		containerIDs := make([]string, 0, len(n.Containers))
		for id := range n.Containers {
			containerIDs = append(containerIDs, id)
		}
		summaries = append(summaries, NetworkSummary{
			ID:           n.ID,
			Name:         n.Name,
			Driver:       n.Driver,
			Scope:        n.Scope,
			Internal:     n.Internal,
			Labels:       n.Labels,
			ContainerIDs: containerIDs,
		})
	}
	return summaries, nil
}

// CreateNetwork creates a new user-defined network. driver "" lets Docker
// pick its default (bridge).
func CreateNetwork(ctx context.Context, cli *client.Client, name, driver string, internal bool, labels map[string]string) (string, error) {
	resp, err := cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver:   driver,
		Internal: internal,
		Labels:   labels,
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// RemoveNetwork removes networkID.
func RemoveNetwork(ctx context.Context, cli *client.Client, networkID string) error {
	err := cli.NetworkRemove(ctx, networkID)
	if err != nil && errdefs.IsNotFound(err) {
		return errors.New("no such network: " + networkID)
	}
	return err
}

// ConnectContainerToNetwork attaches containerID to networkID.
func ConnectContainerToNetwork(ctx context.Context, cli *client.Client, networkID, containerID string) error {
	return cli.NetworkConnect(ctx, networkID, containerID, nil)
}

// DisconnectContainerFromNetwork detaches containerID from networkID,
// force-detaching (bypassing Docker's normal endpoint cleanup) when force
// is set — for a container that's already gone or stuck.
func DisconnectContainerFromNetwork(ctx context.Context, cli *client.Client, networkID, containerID string, force bool) error {
	return cli.NetworkDisconnect(ctx, networkID, containerID, force)
}
