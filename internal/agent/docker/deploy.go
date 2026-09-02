package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/go-connections/nat"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/backoff"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// pullRetryAttempts/pullRetryBase/pullRetryCap govern retrying a transient
// pull failure — edge networks are the flakiest link in this whole system,
// and today a single registry blip aborts the entire deploy with no retry.
// Small base/cap relative to the session-level reconnect backoff, since
// this is within one deploy attempt, not a long-lived connection.
const (
	pullRetryAttempts = 3
	pullRetryBase     = 1 * time.Second
	pullRetryCap      = 8 * time.Second
)

// Docker labels applied to every resource this agent creates, so a
// heartbeat can correlate running containers back to a deployment and a
// future redeploy/teardown command can find what to touch.
const (
	labelDeploymentID = "pspocketedge.deployment_id"
	labelStack        = "pspocketedge.stack"
	// labelManaged marks a standalone container created directly through
	// container management (ops.go) — not part of a deployed stack, so it
	// carries no labelDeploymentID and shows as "Unmanaged" in the fleet
	// UI's existing grouping.
	labelManaged = "pspocketedge.managed"
)

// StatusFunc reports a phase transition back to the control plane over the
// agent's Session stream.
type StatusFunc func(phase agentv1.DeployPhase, message string)

// Deploy parses cmd's compose YAML with compose-go and applies it via the
// Docker Engine SDK: pull images, ensure a per-deployment network, create
// and start containers. Redeploy is "recreate" — any containers already
// labeled with this deployment_id are stopped and removed first, so this
// same function handles both first-deploy and redeploy.
//
// MVP scope: no partial-failure rollback (see the plan's risk notes) — on
// error, whatever was already created is left in place for inspection, and
// FAILED is reported with the error detail.
func Deploy(ctx context.Context, cli *client.Client, cmd *agentv1.DeployStackCommand, report StatusFunc) error {
	project, err := parseCompose(ctx, cmd.GetComposeYaml(), cmd.GetEnv(), cmd.GetStackName())
	if err != nil {
		report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "failed to parse compose file: "+err.Error())
		return err
	}

	report(agentv1.DeployPhase_DEPLOY_PHASE_PULLING, "")
	for name, svc := range project.Services {
		if err := pullImage(ctx, cli, svc.Image); err != nil {
			report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, fmt.Sprintf("failed to pull image for service %q: %v", name, err))
			return err
		}
	}

	report(agentv1.DeployPhase_DEPLOY_PHASE_CREATING, "")

	if err := removeExisting(ctx, cli, cmd.GetDeploymentId()); err != nil {
		report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "failed to remove previous containers: "+err.Error())
		return err
	}

	networkName, err := ensureNetwork(ctx, cli, cmd.GetDeploymentId(), cmd.GetStackName())
	if err != nil {
		report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "failed to create network: "+err.Error())
		return err
	}

	// Named volumes are created once and never removed by removeExisting
	// above — that's what makes redeploy actually preserve data (a
	// database's rows, a model cache) instead of starting fresh every
	// time, which is the whole point of supporting them for stateful
	// catalog entries like Postgres/Ollama/Qdrant.
	volumeNames, err := ensureVolumes(ctx, cli, cmd.GetDeploymentId(), cmd.GetStackName(), project)
	if err != nil {
		report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "failed to create volumes: "+err.Error())
		return err
	}

	for name, svc := range project.Services {
		containerID, err := createContainer(ctx, cli, cmd.GetDeploymentId(), cmd.GetStackName(), name, svc, networkName, volumeNames)
		if err != nil {
			report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, fmt.Sprintf("failed to create container for service %q: %v", name, err))
			return err
		}
		if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
			report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, fmt.Sprintf("failed to start container for service %q: %v", name, err))
			return err
		}
	}

	report(agentv1.DeployPhase_DEPLOY_PHASE_RUNNING, "")
	return nil
}

func parseCompose(ctx context.Context, composeYAML string, env map[string]string, stackName string) (*types.Project, error) {
	details := types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{
			{Filename: "compose.yaml", Content: []byte(composeYAML)},
		},
		Environment: env,
	}
	name := sanitizeProjectName(stackName)
	return loader.LoadWithContext(ctx, details, func(o *loader.Options) {
		o.SetProjectName(name, true)
		o.SkipConsistencyCheck = true
	})
}

var invalidProjectNameChars = regexp.MustCompile(`[^a-z0-9_-]+`)

// sanitizeProjectName maps a stack name to a valid compose project name
// (lowercase alphanumeric, '-', '_'), which also becomes the resource name
// prefix compose-go generates for networks/volumes.
func sanitizeProjectName(name string) string {
	s := invalidProjectNameChars.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-_")
	if s == "" {
		return "stack"
	}
	return s
}

// pullImage defers the network pull entirely if ref is already present
// locally (a redeploy of an unchanged base image shouldn't re-pull it over
// what might be a slow or metered edge connection), then retries a genuine
// pull up to pullRetryAttempts times with jittered backoff — a transient
// registry blip on a flaky edge connection otherwise aborts the whole
// deploy with no retry.
func pullImage(ctx context.Context, cli *client.Client, ref string) error {
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}

	delay := pullRetryBase
	var lastErr error
	for attempt := 1; attempt <= pullRetryAttempts; attempt++ {
		if lastErr = pullImageOnce(ctx, cli, ref); lastErr == nil {
			return nil
		}
		if attempt == pullRetryAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff.Jitter(delay)):
		}
		delay *= 2
		if delay > pullRetryCap {
			delay = pullRetryCap
		}
	}
	return fmt.Errorf("pull failed after %d attempts: %w", pullRetryAttempts, lastErr)
}

// pullImageOnce drains the ImagePull response and surfaces registry-side
// failures. The HTTP response streams newline-delimited JSON progress
// messages and still closes cleanly (err == nil from a bare io.Copy) even
// when an individual message reports a failure (rate limit, manifest not
// found, auth failure, ...) — Docker's API reports those failures inside
// the stream, not as a request-level error. A bare io.Copy would swallow
// that silently and let deploy proceed to ContainerCreate against an image
// that was never actually pulled.
//
// Belt-and-suspenders: a flaky registry connection can also make the
// stream end cleanly (a plain EOF, no error message) after stalling
// partway through — observed directly while building this, pulling a
// multi-GB image over a slow link — so a clean pullImageOnce return still
// isn't proof the image exists. ImageInspect confirms it actually landed
// before Deploy proceeds to ContainerCreate, instead of surfacing Docker's
// more confusing "No such image" error from that later, unrelated call.
func pullImageOnce(ctx context.Context, cli *client.Client, ref string) error {
	reader, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()

	decoder := json.NewDecoder(reader)
	for {
		var msg jsonmessage.JSONMessage
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if msg.Error != nil {
			return msg.Error
		}
	}

	if _, err := cli.ImageInspect(ctx, ref); err != nil {
		return fmt.Errorf("pull reported success but image is not present locally: %w", err)
	}
	return nil
}

// removeExisting implements the "redeploy = recreate" idempotency rule:
// stop and remove any containers already labeled with this deployment_id
// before creating fresh ones.
func removeExisting(ctx context.Context, cli *client.Client, deploymentID string) error {
	f := filters.NewArgs(filters.Arg("label", labelDeploymentID+"="+deploymentID))
	existing, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return err
	}
	for _, c := range existing {
		if err := stopAndRemoveContainer(ctx, cli, c.ID); err != nil {
			return err
		}
	}
	return nil
}

// stopAndRemoveContainer gracefully stops (with a short timeout) then
// force-removes containerID. Shared by removeExisting (redeploy's
// recreate-all-matching-containers path) and RecreateContainer (ops.go's
// single-container recreate).
func stopAndRemoveContainer(ctx context.Context, cli *client.Client, containerID string) error {
	timeout := 10
	_ = cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
	return cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
}

// ensureNetwork creates (or reuses) a bridge network scoped to this
// deployment, so multi-service stacks can reach each other by service name.
func ensureNetwork(ctx context.Context, cli *client.Client, deploymentID, stackName string) (string, error) {
	name := "pe-" + deploymentID

	existing, err := cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(filters.Arg("name", name))})
	if err != nil {
		return "", err
	}
	for _, n := range existing {
		if n.Name == name {
			return name, nil
		}
	}

	_, err = cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			labelDeploymentID: deploymentID,
			labelStack:        stackName,
		},
	})
	return name, err
}

// ensureVolumes creates (idempotently) one Docker named volume per named
// volume declared in the compose project, scoped to this deployment_id,
// and returns a map from the compose-file volume name (e.g. "data") to the
// actual Docker volume name (e.g. "pe-<deployment_id>-data"). Also used by
// Restore (see restore.go) to recreate the same volumes before extracting
// a backup into them.
//
// Bind mounts are deliberately not supported: a host path in a
// marketplace-sourced compose file would let a catalog entry read/write
// arbitrary paths on the agent's host, which is a real privilege-escalation
// surface this MVP doesn't attempt to sandbox against. Named volumes avoid
// that because Docker manages their storage location itself.
func ensureVolumes(ctx context.Context, cli *client.Client, deploymentID, stackName string, project *types.Project) (map[string]string, error) {
	resolved := make(map[string]string, len(project.Volumes))
	for name := range project.Volumes {
		dockerName := "pe-" + deploymentID + "-" + name
		if _, err := cli.VolumeInspect(ctx, dockerName); err == nil {
			resolved[name] = dockerName
			continue
		} else if !errdefs.IsNotFound(err) {
			return nil, err
		}

		if _, err := cli.VolumeCreate(ctx, volume.CreateOptions{
			Name: dockerName,
			Labels: map[string]string{
				labelDeploymentID: deploymentID,
				labelStack:        stackName,
			},
		}); err != nil {
			return nil, err
		}
		resolved[name] = dockerName
	}
	return resolved, nil
}

func createContainer(ctx context.Context, cli *client.Client, deploymentID, stackName, serviceName string, svc types.ServiceConfig, networkName string, volumeNames map[string]string) (string, error) {
	env := make([]string, 0, len(svc.Environment))
	for k, v := range svc.Environment {
		if v != nil {
			env = append(env, k+"="+*v)
		} else {
			env = append(env, k)
		}
	}

	exposedPorts, portBindings, err := buildPorts(svc.Ports)
	if err != nil {
		return "", err
	}

	labels := map[string]string{
		labelDeploymentID: deploymentID,
		labelStack:        stackName,
	}
	for k, v := range svc.Labels {
		labels[k] = v
	}

	containerName := "pe-" + deploymentID + "-" + serviceName

	mounts, err := buildMounts(svc.Volumes, volumeNames)
	if err != nil {
		return "", err
	}

	config := &container.Config{
		Image:        svc.Image,
		Env:          env,
		Cmd:          strslice.StrSlice(svc.Command),
		Labels:       labels,
		ExposedPorts: exposedPorts,
	}
	hostConfig := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: parseRestartPolicy(svc.Restart),
		Mounts:        mounts,
	}
	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {Aliases: []string{serviceName}},
		},
	}

	resp, err := cli.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, containerName)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// buildMounts translates a service's compose `volumes:` entries into
// Docker mounts. Only named volumes are supported (see ensureVolumes'
// doc comment on why bind mounts are deliberately rejected rather than
// silently ignored) — an unsupported entry is a hard error, not a
// best-effort skip, so a catalog entry that needs a bind mount fails
// loudly at deploy time instead of silently losing its data directory.
func buildMounts(volumes []types.ServiceVolumeConfig, volumeNames map[string]string) ([]mount.Mount, error) {
	mounts := make([]mount.Mount, 0, len(volumes))
	for _, v := range volumes {
		if v.Type != "volume" {
			return nil, fmt.Errorf("unsupported volume type %q for %q (only named volumes are supported)", v.Type, v.Target)
		}
		dockerName, ok := volumeNames[v.Source]
		if !ok {
			return nil, fmt.Errorf("volume %q not declared in the compose file's top-level volumes section", v.Source)
		}
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeVolume,
			Source:   dockerName,
			Target:   v.Target,
			ReadOnly: v.ReadOnly,
		})
	}
	return mounts, nil
}

func buildPorts(ports []types.ServicePortConfig) (nat.PortSet, nat.PortMap, error) {
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	for _, p := range ports {
		if err := addPortBinding(exposed, bindings, p.Protocol, p.Target, p.HostIP, p.Published); err != nil {
			return nil, nil, err
		}
	}
	return exposed, bindings, nil
}

// addPortBinding computes containerPort's nat.Port and records it as
// exposed, plus a host binding if hostPort is non-empty. Shared by
// buildPorts (compose-derived ports, deploy.go) and buildPortsFromSpec
// (standalone-container ports, ops.go) so both go through the same
// nat.Port construction.
func addPortBinding(exposed nat.PortSet, bindings nat.PortMap, protocol string, containerPort uint32, hostIP, hostPort string) error {
	if protocol == "" {
		protocol = "tcp"
	}
	port, err := nat.NewPort(protocol, fmt.Sprintf("%d", containerPort))
	if err != nil {
		return err
	}
	exposed[port] = struct{}{}
	if hostPort != "" {
		bindings[port] = append(bindings[port], nat.PortBinding{HostIP: hostIP, HostPort: hostPort})
	}
	return nil
}

func parseRestartPolicy(restart string) container.RestartPolicy {
	switch restart {
	case "always":
		return container.RestartPolicy{Name: container.RestartPolicyAlways}
	case "on-failure":
		return container.RestartPolicy{Name: container.RestartPolicyOnFailure}
	case "unless-stopped":
		return container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	default:
		return container.RestartPolicy{Name: container.RestartPolicyDisabled}
	}
}
