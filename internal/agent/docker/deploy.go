package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
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
// agent's Session stream. service is empty for a whole-stack transition and
// set for one service's progress (image pulled, container started, ...).
type StatusFunc func(phase agentv1.DeployPhase, service, message string)

// Deploy parses cmd's compose YAML with compose-go and applies it via the
// Docker Engine SDK: pull images, ensure the deployment's networks and
// volumes, then create and start every service's containers in dependency
// order (depends_on), honoring service_healthy/service_completed_successfully
// conditions and per-service replica counts. Progress is reported per
// service as well as for the stack as a whole.
//
// cmd.Strategy picks how an existing deployment is replaced:
//
//   - "recreate" (default): every old container is removed first, then the
//     new ones are created. Partial-failure cleanup: once removeExisting has
//     run we've committed to replacing the stack, so a later failure removes
//     whatever this attempt created (and the deployment's networks),
//     leaving a clean "nothing running" state instead of a half-built stack.
//     Named volumes are deliberately left alone (see docker.Undeploy's doc
//     comment) so a failed redeploy can never destroy data, and a failure
//     before removeExisting (compose parse, image pull) leaves the previous
//     deployment untouched and running.
//   - "rolling": see rollingUpdate — containers are replaced one at a time
//     and the previous containers are restored if any replacement fails to
//     become ready.
//
// After the stack is running, VerifyDeployment runs as post-deployment
// health verification unless cmd.VerifyTimeoutSeconds is negative, and its
// outcome is reported as HEALTHY/UNHEALTHY. An unhealthy result doesn't
// make Deploy return an error — the containers are up, just not healthy —
// so the control plane decides what to do about it (e.g. auto-rollback).
func Deploy(ctx context.Context, cli *client.Client, cmd *agentv1.DeployStackCommand, report StatusFunc) (err error) {
	deploymentID := cmd.GetDeploymentId()

	project, err := parseCompose(ctx, cmd.GetComposeYaml(), cmd.GetEnv(), cmd.GetStackName())
	if err != nil {
		report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "", "failed to parse compose file: "+err.Error())
		return err
	}
	order, err := ServiceOrder(project)
	if err != nil {
		report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "", err.Error())
		return err
	}
	replicas := make(map[string]int, len(order))
	for _, name := range order {
		svc := project.Services[name]
		replicas[name] = DesiredReplicas(svc, cmd.GetReplicas()[name])
		if err = checkScalable(name, svc, replicas[name]); err != nil {
			report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, name, err.Error())
			return err
		}
	}

	report(agentv1.DeployPhase_DEPLOY_PHASE_PULLING, "", "")
	for _, name := range order {
		svc := project.Services[name]
		report(agentv1.DeployPhase_DEPLOY_PHASE_PULLING, name, "pulling "+svc.Image)
		if err = pullImage(ctx, cli, svc.Image); err != nil {
			msg := fmt.Sprintf("failed to pull image for service %q: %v", name, err)
			report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, name, msg)
			report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "", msg)
			return err
		}
		report(agentv1.DeployPhase_DEPLOY_PHASE_PULLING, name, "image ready")
	}

	report(agentv1.DeployPhase_DEPLOY_PHASE_CREATING, "", "")
	if cmd.GetStrategy() == "rolling" {
		err = rollingUpdate(ctx, cli, cmd, project, order, replicas, report)
	} else {
		err = recreateStack(ctx, cli, cmd, project, order, replicas, report)
	}
	if err != nil {
		var rb *rolledBackError
		if errors.As(err, &rb) {
			report(agentv1.DeployPhase_DEPLOY_PHASE_ROLLED_BACK, "", err.Error())
		} else {
			report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, "", err.Error())
		}
		return err
	}

	report(agentv1.DeployPhase_DEPLOY_PHASE_RUNNING, "", "")

	if cmd.GetVerifyTimeoutSeconds() >= 0 {
		timeout := DefaultVerifyTimeout
		if cmd.GetVerifyTimeoutSeconds() > 0 {
			timeout = time.Duration(cmd.GetVerifyTimeoutSeconds()) * time.Second
		}
		report(agentv1.DeployPhase_DEPLOY_PHASE_VERIFYING, "", fmt.Sprintf("waiting up to %s for every container to be running and healthy", timeout))
		if verr := VerifyDeployment(ctx, cli, deploymentID, project, timeout); verr != nil {
			report(agentv1.DeployPhase_DEPLOY_PHASE_UNHEALTHY, "", verr.Error())
		} else {
			report(agentv1.DeployPhase_DEPLOY_PHASE_HEALTHY, "", "all containers running and healthy")
		}
	}
	return nil
}

// recreateStack is Deploy's "recreate" strategy — see Deploy's doc comment.
func recreateStack(ctx context.Context, cli *client.Client, cmd *agentv1.DeployStackCommand, project *types.Project, order []string, replicas map[string]int, report StatusFunc) (err error) {
	deploymentID := cmd.GetDeploymentId()
	var committed bool
	defer func() {
		if err != nil && committed {
			_ = removeExisting(ctx, cli, deploymentID)
			_ = removeDeploymentNetworks(ctx, cli, deploymentID)
		}
	}()

	if err = removeExisting(ctx, cli, deploymentID); err != nil {
		return fmt.Errorf("failed to remove previous containers: %w", err)
	}
	committed = true

	networks, err := ensureNetworks(ctx, cli, deploymentID, cmd.GetStackName(), project)
	if err != nil {
		return fmt.Errorf("failed to create networks: %w", err)
	}

	// Named volumes are created once and never removed by removeExisting
	// above — that's what makes redeploy actually preserve data (a
	// database's rows, a model cache) instead of starting fresh every
	// time, which is the whole point of supporting them for stateful
	// catalog entries like Postgres/Ollama/Qdrant.
	volumeNames, err := ensureVolumes(ctx, cli, deploymentID, cmd.GetStackName(), project)
	if err != nil {
		return fmt.Errorf("failed to create volumes: %w", err)
	}

	for _, name := range order {
		svc := project.Services[name]
		if err = waitForDependencies(ctx, cli, deploymentID, svc); err != nil {
			report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, name, err.Error())
			return fmt.Errorf("service %q: %w", name, err)
		}
		report(agentv1.DeployPhase_DEPLOY_PHASE_CREATING, name, "creating")
		n := replicas[name]
		for i := 1; i <= n; i++ {
			var containerID string
			containerID, err = createServiceContainer(ctx, cli, deploymentID, cmd.GetStackName(), name, i, svc, networks, volumeNames)
			if err != nil {
				report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, name, err.Error())
				return fmt.Errorf("failed to create container for service %q: %w", name, err)
			}
			if err = cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
				report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, name, err.Error())
				return fmt.Errorf("failed to start container for service %q: %w", name, err)
			}
		}
		report(agentv1.DeployPhase_DEPLOY_PHASE_RUNNING, name, fmt.Sprintf("started %d container(s)", n))
	}
	return nil
}

// rolledBackError is a rolling update failure after which the previous
// containers were restored — the stack is still running its previous
// definition, which Deploy reports as ROLLED_BACK rather than FAILED.
type rolledBackError struct{ msg string }

func (e *rolledBackError) Error() string { return e.msg }

// replacedContainer records one container a rolling update swapped out, so
// the swap can be undone if a later replacement fails.
type replacedContainer struct {
	name     string
	newID    string
	prevID   string
	prevName string
}

// rollingUpdate is Deploy's "rolling" strategy — the controlled update.
// Services are updated in dependency order, one replica at a time: the old
// container is renamed aside and stopped (freeing any host port it
// publishes), the replacement is created and started, and the update waits
// for it to become ready (healthy, or running stably when it has no
// healthcheck). If any replacement fails, every replacement made so far is
// removed and the previous containers are renamed back and restarted — the
// stack ends up exactly as it was. Only once every service is updated are
// the previous containers, and containers of services or replicas the new
// Compose file no longer has, removed.
func rollingUpdate(ctx context.Context, cli *client.Client, cmd *agentv1.DeployStackCommand, project *types.Project, order []string, replicas map[string]int, report StatusFunc) error {
	deploymentID := cmd.GetDeploymentId()
	networks, err := ensureNetworks(ctx, cli, deploymentID, cmd.GetStackName(), project)
	if err != nil {
		return fmt.Errorf("failed to create networks: %w", err)
	}
	volumeNames, err := ensureVolumes(ctx, cli, deploymentID, cmd.GetStackName(), project)
	if err != nil {
		return fmt.Errorf("failed to create volumes: %w", err)
	}

	existing, err := deploymentContainers(ctx, cli, deploymentID)
	if err != nil {
		return err
	}
	byName := make(map[string]serviceContainer, len(existing))
	for _, c := range existing {
		byName[c.Name] = c
	}

	var swapped []replacedContainer
	rollback := func() {
		for i := len(swapped) - 1; i >= 0; i-- {
			s := swapped[i]
			if s.newID != "" {
				_ = cli.ContainerRemove(ctx, s.newID, container.RemoveOptions{Force: true})
			}
			if s.prevID != "" {
				_ = cli.ContainerRename(ctx, s.prevID, s.name)
				_ = cli.ContainerStart(ctx, s.prevID, container.StartOptions{})
			}
		}
	}

	for _, name := range order {
		svc := project.Services[name]
		if err := waitForDependencies(ctx, cli, deploymentID, svc); err != nil {
			rollback()
			report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, name, err.Error())
			return &rolledBackError{msg: fmt.Sprintf("rolling update stopped at service %q (%v); previous containers restored", name, err)}
		}
		n := replicas[name]
		for i := 1; i <= n; i++ {
			cname := containerName(deploymentID, name, i)
			entry := replacedContainer{name: cname}
			if old, ok := byName[cname]; ok {
				entry.prevName = cname + setAsideSuffix
				// A leftover from an interrupted earlier rollout.
				if stale, ok := byName[entry.prevName]; ok {
					_ = stopAndRemoveContainer(ctx, cli, stale.ID)
					delete(byName, entry.prevName)
				}
				if err := cli.ContainerRename(ctx, old.ID, entry.prevName); err != nil {
					rollback()
					return fmt.Errorf("service %q: failed to set aside previous container: %w", name, err)
				}
				entry.prevID = old.ID
				delete(byName, cname)
				timeout := 10
				_ = cli.ContainerStop(ctx, old.ID, container.StopOptions{Timeout: &timeout})
			}
			report(agentv1.DeployPhase_DEPLOY_PHASE_CREATING, name, fmt.Sprintf("updating replica %d/%d", i, n))
			newID, err := createServiceContainer(ctx, cli, deploymentID, cmd.GetStackName(), name, i, svc, networks, volumeNames)
			if err == nil {
				entry.newID = newID
				err = cli.ContainerStart(ctx, newID, container.StartOptions{})
			}
			swapped = append(swapped, entry)
			if err == nil {
				err = waitReady(ctx, cli, entry.newID, rollingReadyTimeout, false)
				if err != nil && strings.HasPrefix(err.Error(), "container exited with code 0") && isOneShot(svc) {
					err = nil
				}
			}
			if err != nil {
				rollback()
				msg := fmt.Sprintf("replica %d failed to become ready (%v); rolled back to the previous containers", i, err)
				report(agentv1.DeployPhase_DEPLOY_PHASE_FAILED, name, msg)
				return &rolledBackError{msg: fmt.Sprintf("rolling update of service %q failed (%v); previous containers restored", name, err)}
			}
		}
		report(agentv1.DeployPhase_DEPLOY_PHASE_RUNNING, name, fmt.Sprintf("updated %d container(s)", n))
	}

	for _, s := range swapped {
		if s.prevID != "" {
			_ = stopAndRemoveContainer(ctx, cli, s.prevID)
		}
	}
	// Whatever's left belonged to services removed from the Compose file
	// or replicas beyond the new count.
	for _, c := range byName {
		_ = stopAndRemoveContainer(ctx, cli, c.ID)
	}
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

// Undeploy tears down a deployment entirely: stops and removes every
// container labeled with deploymentID (removeExisting, the same helper
// redeploy uses before recreating), then removes the networks created for
// it (the default per-deployment one and any declared custom networks). Named volumes are deliberately
// left in place — matching `docker compose down`'s default (no -v) — so a
// stack removal can't silently destroy data the backup/restore feature is
// meant to protect.
func Undeploy(ctx context.Context, cli *client.Client, deploymentID string) error {
	if err := removeExisting(ctx, cli, deploymentID); err != nil {
		return err
	}
	return removeDeploymentNetworks(ctx, cli, deploymentID)
}

// DeployService redeploys or scales a single service within an
// already-deployed stack, leaving every other service's containers
// untouched. replicas is the number of containers to run (0 = the
// Compose-declared count, or 1).
//
// scaleOnly=false (redeploy): pulls the image, then removes and recreates
// every container of the service. scaleOnly=true (scale): keeps existing
// replicas running as-is, creating only the missing ones and removing any
// beyond the new count — highest replica numbers first.
//
// The networks and named volumes are ensured first (idempotently, same as
// Deploy) since a new container still needs to reach them.
func DeployService(ctx context.Context, cli *client.Client, deploymentID, stackName, composeYAML string, env map[string]string, serviceName string, replicas int, scaleOnly bool) error {
	project, err := parseCompose(ctx, composeYAML, env, stackName)
	if err != nil {
		return err
	}
	svc, ok := project.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %q not found in compose file", serviceName)
	}
	n := DesiredReplicas(svc, int32(replicas))
	if err := checkScalable(serviceName, svc, n); err != nil {
		return err
	}

	if !scaleOnly {
		if err := pullImage(ctx, cli, svc.Image); err != nil {
			return err
		}
	}

	networks, err := ensureNetworks(ctx, cli, deploymentID, stackName, project)
	if err != nil {
		return err
	}
	volumeNames, err := ensureVolumes(ctx, cli, deploymentID, stackName, project)
	if err != nil {
		return err
	}

	all, err := deploymentContainers(ctx, cli, deploymentID)
	if err != nil {
		return err
	}
	existing := map[int]serviceContainer{}
	for _, c := range all {
		if c.SetAside {
			// Left over from an interrupted rolling update.
			if c.Service == serviceName {
				_ = stopAndRemoveContainer(ctx, cli, c.ID)
			}
			continue
		}
		if c.Service == serviceName {
			existing[c.Replica] = c
		}
	}

	for replica, c := range existing {
		if !scaleOnly || replica > n {
			if err := stopAndRemoveContainer(ctx, cli, c.ID); err != nil {
				return err
			}
			delete(existing, replica)
		}
	}

	for i := 1; i <= n; i++ {
		if c, ok := existing[i]; ok {
			if !c.Running {
				if err := cli.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
					return err
				}
			}
			continue
		}
		containerID, err := createServiceContainer(ctx, cli, deploymentID, stackName, serviceName, i, svc, networks, volumeNames)
		if err != nil {
			return err
		}
		if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
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

func createContainer(ctx context.Context, cli *client.Client, deploymentID, stackName, serviceName string, replica int, svc types.ServiceConfig, networkName string, endpoint *network.EndpointSettings, volumeNames map[string]string) (string, error) {
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

	labels := map[string]string{}
	for k, v := range svc.Labels {
		labels[k] = v
	}
	labels[labelDeploymentID] = deploymentID
	labels[labelStack] = stackName
	labels[labelService] = serviceName
	labels[labelReplica] = strconv.Itoa(replica)

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
		Healthcheck:  healthCheckFromCompose(svc.HealthCheck),
	}
	hostConfig := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: parseRestartPolicy(svc.Restart),
		Mounts:        mounts,
		Resources:     resourcesFromDeploy(svc.Deploy),
	}
	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: endpoint,
		},
	}

	resp, err := cli.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, containerName(deploymentID, serviceName, replica))
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

// resourcesFromDeploy translates a compose service's deploy.resources
// (limits/reservations) into Docker's container.Resources — the same
// nanoCPUs/bytes shape resourcesFromConfig (ops.go) already uses for
// standalone containers' UpdateResourceLimits, so a Compose-declared
// limit behaves identically to one set afterward via the Containers UI.
// A service with no "deploy:" section (the common case) gets a zero
// value, i.e. unlimited.
func resourcesFromDeploy(deploy *types.DeployConfig) container.Resources {
	if deploy == nil {
		return container.Resources{}
	}
	var res container.Resources
	if limits := deploy.Resources.Limits; limits != nil {
		res.NanoCPUs = int64(float64(limits.NanoCPUs) * 1e9)
		res.Memory = int64(limits.MemoryBytes)
	}
	if reservations := deploy.Resources.Reservations; reservations != nil {
		res.MemoryReservation = int64(reservations.MemoryBytes)
	}
	return res
}

// healthCheckFromCompose translates a compose service's healthcheck
// config into Docker's container.HealthConfig. A service with no
// "healthcheck:" section (the common case) gets a nil config, meaning
// "use whatever HEALTHCHECK the image itself declares, if any" — the same
// default Docker applies when a container is created without one.
func healthCheckFromCompose(hc *types.HealthCheckConfig) *container.HealthConfig {
	if hc == nil {
		return nil
	}
	if hc.Disable {
		return &container.HealthConfig{Test: []string{"NONE"}}
	}
	cfg := &container.HealthConfig{Test: []string(hc.Test)}
	if hc.Interval != nil {
		cfg.Interval = time.Duration(*hc.Interval)
	}
	if hc.Timeout != nil {
		cfg.Timeout = time.Duration(*hc.Timeout)
	}
	if hc.StartPeriod != nil {
		cfg.StartPeriod = time.Duration(*hc.StartPeriod)
	}
	if hc.StartInterval != nil {
		cfg.StartInterval = time.Duration(*hc.StartInterval)
	}
	if hc.Retries != nil {
		cfg.Retries = int(*hc.Retries)
	}
	return cfg
}
