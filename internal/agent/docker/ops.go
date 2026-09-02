package docker

import (
	"context"
	"fmt"
	"strconv"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// ContainerAction applies one lifecycle operation to an existing
// container. timeoutSeconds governs the graceful window for STOP/RESTART
// (<=0 uses the Docker Engine default); force governs whether REMOVE is
// allowed against a still-running container.
func ContainerAction(ctx context.Context, cli *client.Client, containerID string, action agentv1.ContainerAction, timeoutSeconds int32, force bool) error {
	switch action {
	case agentv1.ContainerAction_CONTAINER_ACTION_START:
		return cli.ContainerStart(ctx, containerID, container.StartOptions{})
	case agentv1.ContainerAction_CONTAINER_ACTION_STOP:
		return cli.ContainerStop(ctx, containerID, stopOptions(timeoutSeconds))
	case agentv1.ContainerAction_CONTAINER_ACTION_RESTART:
		return cli.ContainerRestart(ctx, containerID, stopOptions(timeoutSeconds))
	case agentv1.ContainerAction_CONTAINER_ACTION_PAUSE:
		return cli.ContainerPause(ctx, containerID)
	case agentv1.ContainerAction_CONTAINER_ACTION_RESUME:
		return cli.ContainerUnpause(ctx, containerID)
	case agentv1.ContainerAction_CONTAINER_ACTION_KILL:
		// Force-kill: SIGKILL, no grace period, for an unresponsive
		// container that STOP's graceful signal won't reach.
		return cli.ContainerKill(ctx, containerID, "SIGKILL")
	case agentv1.ContainerAction_CONTAINER_ACTION_REMOVE:
		return cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: force})
	default:
		return fmt.Errorf("unsupported container action: %v", action)
	}
}

func stopOptions(timeoutSeconds int32) container.StopOptions {
	if timeoutSeconds <= 0 {
		return container.StopOptions{}
	}
	t := int(timeoutSeconds)
	return container.StopOptions{Timeout: &t}
}

// CreateContainer pulls cfg.Image if needed, creates a standalone
// container (not part of a deployed stack — see labelManaged) from cfg,
// and starts it.
func CreateContainer(ctx context.Context, cli *client.Client, cfg ContainerConfig) (string, error) {
	id, err := createStandaloneContainer(ctx, cli, cfg)
	if err != nil {
		return "", err
	}
	if err := cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return id, err
	}
	return id, nil
}

// RenameContainer renames an existing container.
func RenameContainer(ctx context.Context, cli *client.Client, containerID, newName string) error {
	return cli.ContainerRename(ctx, containerID, newName)
}

// CloneContainer duplicates containerID's configuration (image, command,
// env, ports, named-volume mounts, restart policy, and any non-internal
// labels) into a new container named newName. Deliberately not
// auto-started — starting it immediately would race the original for any
// host ports both declare.
func CloneContainer(ctx context.Context, cli *client.Client, containerID, newName string) (string, error) {
	info, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	return createStandaloneContainer(ctx, cli, configFromInspect(info, newName))
}

// RecreateContainer stops and removes containerID, then creates and starts
// a fresh container from cfg (the caller decides whether cfg is the old
// config with edits applied, or something new entirely).
func RecreateContainer(ctx context.Context, cli *client.Client, containerID string, cfg ContainerConfig) (string, error) {
	if err := stopAndRemoveContainer(ctx, cli, containerID); err != nil {
		return "", fmt.Errorf("failed to remove existing container: %w", err)
	}
	return CreateContainer(ctx, cli, cfg)
}

// UpdateRestartPolicy changes containerID's restart policy in place — the
// Docker Engine API supports this without recreating the container.
func UpdateRestartPolicy(ctx context.Context, cli *client.Client, containerID, name string, maxRetry int32) error {
	_, err := cli.ContainerUpdate(ctx, containerID, container.UpdateConfig{
		RestartPolicy: restartPolicyFromConfig(name, int(maxRetry)),
	})
	return err
}

// UpdateResourceLimits changes containerID's CPU/memory/process limits in
// place via the same ContainerUpdate call UpdateRestartPolicy uses — no
// recreate needed. 0 on any field clears that limit.
func UpdateResourceLimits(ctx context.Context, cli *client.Client, containerID string, nanoCPUs, memoryLimitBytes, memoryReservationBytes, pidsLimit int64) error {
	_, err := cli.ContainerUpdate(ctx, containerID, container.UpdateConfig{
		Resources: resourcesFromConfig(ContainerConfig{
			NanoCPUs:               nanoCPUs,
			MemoryLimitBytes:       memoryLimitBytes,
			MemoryReservationBytes: memoryReservationBytes,
			PidsLimit:              pidsLimit,
		}),
	})
	return err
}

// createStandaloneContainer builds and creates (but does not start) a
// container from cfg. Shared by CreateContainer (starts it immediately)
// and CloneContainer (deliberately does not).
func createStandaloneContainer(ctx context.Context, cli *client.Client, cfg ContainerConfig) (string, error) {
	if err := pullImage(ctx, cli, cfg.Image); err != nil {
		return "", fmt.Errorf("failed to pull image: %w", err)
	}

	exposedPorts, portBindings, err := buildPortsFromSpec(cfg.Ports)
	if err != nil {
		return "", err
	}

	mounts, err := buildMountsFromSpec(ctx, cli, cfg.Volumes)
	if err != nil {
		return "", err
	}

	labels := map[string]string{labelManaged: "true"}
	for k, v := range cfg.Labels {
		labels[k] = v
	}

	config := &container.Config{
		Image:        cfg.Image,
		Cmd:          strslice.StrSlice(cfg.Command),
		Env:          cfg.Env,
		Labels:       labels,
		ExposedPorts: exposedPorts,
	}
	hostConfig := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: restartPolicyFromConfig(cfg.RestartPolicyName, cfg.RestartPolicyMaxRetryCount),
		Mounts:        mounts,
		Resources:     resourcesFromConfig(cfg),
	}

	resp, err := cli.ContainerCreate(ctx, config, hostConfig, nil, nil, cfg.Name)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// restartPolicyFromConfig reuses parseRestartPolicy's name mapping
// (deploy.go) and layers the max-retry count on top for "on-failure" —
// the only policy that uses it (see container.ValidateRestartPolicy).
func restartPolicyFromConfig(name string, maxRetry int) container.RestartPolicy {
	policy := parseRestartPolicy(name)
	if policy.Name == container.RestartPolicyOnFailure {
		policy.MaximumRetryCount = maxRetry
	}
	return policy
}

// resourcesFromConfig translates cfg's resource-limit fields into a
// container.Resources — 0 on any field means "not set" (see
// ContainerConfig's doc comment), which for NanoCPUs/Memory/
// MemoryReservation is already Docker's own zero-value-means-unlimited
// convention. PidsLimit needs an explicit pointer since the Engine API
// distinguishes an omitted limit (nil) from an explicit "no limit" (-1).
func resourcesFromConfig(cfg ContainerConfig) container.Resources {
	res := container.Resources{
		NanoCPUs:          cfg.NanoCPUs,
		Memory:            cfg.MemoryLimitBytes,
		MemoryReservation: cfg.MemoryReservationBytes,
	}
	if cfg.PidsLimit > 0 {
		res.PidsLimit = &cfg.PidsLimit
	}
	return res
}

// buildPortsFromSpec is buildPorts' (deploy.go) counterpart for a
// standalone container's directly-specified ports rather than a compose
// service's — both funnel through addPortBinding.
func buildPortsFromSpec(ports []ContainerPortSpec) (nat.PortSet, nat.PortMap, error) {
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	for _, p := range ports {
		hostPort := ""
		if p.HostPort != 0 {
			hostPort = strconv.Itoa(int(p.HostPort))
		}
		if err := addPortBinding(exposed, bindings, p.Protocol, uint32(p.ContainerPort), "", hostPort); err != nil {
			return nil, nil, err
		}
	}
	return exposed, bindings, nil
}

// buildMountsFromSpec ensures each named volume a standalone container
// asks for exists (creating it if not — unlike ensureVolumes in deploy.go,
// there's no owning deployment_id to scope these under) and returns the
// resulting mounts. Named volumes only, same bind-mount rejection as the
// compose deploy path.
func buildMountsFromSpec(ctx context.Context, cli *client.Client, volumes []ContainerVolumeSpec) ([]mount.Mount, error) {
	mounts := make([]mount.Mount, 0, len(volumes))
	for _, v := range volumes {
		if err := ensureStandaloneVolume(ctx, cli, v.VolumeName); err != nil {
			return nil, fmt.Errorf("failed to ensure volume %q: %w", v.VolumeName, err)
		}
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeVolume,
			Source:   v.VolumeName,
			Target:   v.Target,
			ReadOnly: v.ReadOnly,
		})
	}
	return mounts, nil
}

func ensureStandaloneVolume(ctx context.Context, cli *client.Client, name string) error {
	if _, err := cli.VolumeInspect(ctx, name); err == nil {
		return nil
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	_, err := cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Labels: map[string]string{labelManaged: "true"},
	})
	return err
}

// configFromInspect builds a ContainerConfig from a live ContainerInspect,
// for CloneContainer (and available to the control plane's recreate flow
// as the starting point it lets the user edit before submitting). Internal
// pspocketedge labels are stripped since createStandaloneContainer adds
// its own labelManaged, and copying labelDeploymentID/labelStack onto a
// clone of a stack-managed container would misattribute it.
func configFromInspect(info container.InspectResponse, newName string) ContainerConfig {
	cfg := ContainerConfig{Name: newName}

	if info.Config != nil {
		cfg.Image = info.Config.Image
		cfg.Command = []string(info.Config.Cmd)
		cfg.Env = info.Config.Env
		if len(info.Config.Labels) > 0 {
			cfg.Labels = make(map[string]string, len(info.Config.Labels))
			for k, v := range info.Config.Labels {
				if k == labelDeploymentID || k == labelStack || k == labelManaged {
					continue
				}
				cfg.Labels[k] = v
			}
		}
	}

	if info.HostConfig != nil {
		cfg.RestartPolicyName = string(info.HostConfig.RestartPolicy.Name)
		cfg.RestartPolicyMaxRetryCount = info.HostConfig.RestartPolicy.MaximumRetryCount
		cfg.Ports = portSpecsFromBindings(info.HostConfig.PortBindings)
		cfg.NanoCPUs = info.HostConfig.NanoCPUs
		cfg.MemoryLimitBytes = info.HostConfig.Memory
		cfg.MemoryReservationBytes = info.HostConfig.MemoryReservation
		if info.HostConfig.PidsLimit != nil {
			cfg.PidsLimit = *info.HostConfig.PidsLimit
		}
	}

	for _, m := range info.Mounts {
		if m.Type != mount.TypeVolume {
			continue
		}
		cfg.Volumes = append(cfg.Volumes, ContainerVolumeSpec{
			VolumeName: m.Name,
			Target:     m.Destination,
			ReadOnly:   !m.RW,
		})
	}

	return cfg
}

func portSpecsFromBindings(bindings nat.PortMap) []ContainerPortSpec {
	var specs []ContainerPortSpec
	for port, bindingList := range bindings {
		containerPort, err := strconv.Atoi(port.Port())
		if err != nil {
			continue
		}
		if len(bindingList) == 0 {
			specs = append(specs, ContainerPortSpec{ContainerPort: uint16(containerPort), Protocol: port.Proto()})
			continue
		}
		for _, b := range bindingList {
			hostPort, _ := strconv.Atoi(b.HostPort)
			specs = append(specs, ContainerPortSpec{
				ContainerPort: uint16(containerPort),
				HostPort:      uint16(hostPort),
				Protocol:      port.Proto(),
			})
		}
	}
	return specs
}
