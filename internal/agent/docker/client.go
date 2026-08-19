// Package docker wraps the Docker Engine SDK to apply a parsed compose
// stack directly — not by shelling out to `docker compose`, since minimal
// Pi/mini-PC images often ship the Engine without the Compose CLI plugin.
package docker

import "github.com/docker/docker/client"

// NewClient connects to the local Docker Engine (via DOCKER_HOST or the
// default socket) with API version negotiation, so the agent works against
// whatever Engine version happens to be installed on the host.
func NewClient() (*client.Client, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}
