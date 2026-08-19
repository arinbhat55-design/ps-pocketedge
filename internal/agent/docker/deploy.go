package docker

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// Docker labels applied to every resource this agent creates, so a
// heartbeat can correlate running containers back to a deployment and a
// future redeploy/teardown command can find what to touch.
const (
	labelDeploymentID = "pspocketedge.deployment_id"
	labelStack        = "pspocketedge.stack"
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
	project, err := parseCompose(ctx, cmd)
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

	networkName, err := ensureNetwork(ctx, cli, cmd)
	if err != nil {
		report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "failed to create network: "+err.Error())
		return err
	}

	for name, svc := range project.Services {
		containerID, err := createContainer(ctx, cli, cmd, name, svc, networkName)
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

func parseCompose(ctx context.Context, cmd *agentv1.DeployStackCommand) (*types.Project, error) {
	details := types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{
			{Filename: "compose.yaml", Content: []byte(cmd.GetComposeYaml())},
		},
		Environment: cmd.GetEnv(),
	}
	name := sanitizeProjectName(cmd.GetStackName())
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

func pullImage(ctx context.Context, cli *client.Client, ref string) error {
	reader, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()
	_, err = io.Copy(io.Discard, reader)
	return err
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
		timeout := 10
		_ = cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeout})
		if err := cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			return err
		}
	}
	return nil
}

// ensureNetwork creates (or reuses) a bridge network scoped to this
// deployment, so multi-service stacks can reach each other by service name.
func ensureNetwork(ctx context.Context, cli *client.Client, cmd *agentv1.DeployStackCommand) (string, error) {
	name := "pe-" + cmd.GetDeploymentId()

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
			labelDeploymentID: cmd.GetDeploymentId(),
			labelStack:        cmd.GetStackName(),
		},
	})
	return name, err
}

func createContainer(ctx context.Context, cli *client.Client, cmd *agentv1.DeployStackCommand, serviceName string, svc types.ServiceConfig, networkName string) (string, error) {
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
		labelDeploymentID: cmd.GetDeploymentId(),
		labelStack:        cmd.GetStackName(),
	}
	for k, v := range svc.Labels {
		labels[k] = v
	}

	containerName := "pe-" + cmd.GetDeploymentId() + "-" + serviceName

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

func buildPorts(ports []types.ServicePortConfig) (nat.PortSet, nat.PortMap, error) {
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		containerPort, err := nat.NewPort(proto, fmt.Sprintf("%d", p.Target))
		if err != nil {
			return nil, nil, err
		}
		exposed[containerPort] = struct{}{}
		if p.Published != "" {
			bindings[containerPort] = append(bindings[containerPort], nat.PortBinding{
				HostIP:   p.HostIP,
				HostPort: p.Published,
			})
		}
	}
	return exposed, bindings, nil
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
