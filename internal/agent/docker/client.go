// Package docker manages containers through the Docker-compatible API.
// Both Docker Engine and Podman's API service are supported.
package docker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/docker/client"
)

// NewClient retains the default Docker environment configuration.
func NewClient() (*client.Client, error) {
	return NewClientForRuntime("docker", "")
}

// NewClientForRuntime connects to Docker or Podman. An explicit host takes
// precedence over DOCKER_HOST; rootless Podman uses the current user's socket.
func NewClientForRuntime(runtime, host string) (*client.Client, error) {
	if runtime == "" {
		runtime = "docker"
	}
	if runtime != "docker" && runtime != "podman" {
		return nil, fmt.Errorf("unsupported container runtime %q (use docker or podman)", runtime)
	}
	// Resolve the host before applying options, so an invalid inherited
	// DOCKER_HOST cannot defeat an explicit configuration override.
	opts := []client.Opt{client.WithTLSClientConfigFromEnv(), client.WithVersionFromEnv()}
	if host == "" {
		host = os.Getenv("DOCKER_HOST")
	}
	if runtime == "podman" && host == "" {
		if os.Getuid() == 0 {
			host = "unix:///run/podman/podman.sock"
		} else {
			dir := os.Getenv("XDG_RUNTIME_DIR")
			if dir == "" {
				dir = fmt.Sprintf("/run/user/%d", os.Getuid())
			}
			host = "unix://" + filepath.Join(dir, "podman", "podman.sock")
		}
	}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}
	if runtime == "podman" {
		// Podman's compatibility API targets Docker v1.40. Avoid newer Docker
		// features and ignore DOCKER_API_VERSION intended for another engine.
		opts = append(opts, client.WithVersion("1.40"))
	} else {
		opts = append(opts, client.WithAPIVersionNegotiation())
	}
	return client.NewClientWithOpts(opts...)
}
