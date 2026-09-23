package docker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// labelService/labelReplica identify which Compose service (and which
// replica of it, 1-based) a stack container runs, so scaling and rolling
// updates can find a service's containers without parsing names. Containers
// created before these labels existed only ever had one replica, named
// containerName(deploymentID, service, 1) — see serviceContainers.
const (
	labelService = "pspocketedge.service"
	labelReplica = "pspocketedge.replica"
)

const (
	// dependencyWaitTimeout bounds how long a service waits for a
	// depends_on condition (service_healthy/service_completed_successfully)
	// before the deploy fails.
	dependencyWaitTimeout = 2 * time.Minute
	// rollingReadyTimeout bounds how long a rolling update waits for one
	// replacement container to become ready before rolling back.
	rollingReadyTimeout = 2 * time.Minute
	// DefaultVerifyTimeout is how long post-deployment verification waits
	// for every container to be running/healthy when the command doesn't
	// say otherwise.
	DefaultVerifyTimeout = 90 * time.Second
	// stableRunningFor is how long a container without a healthcheck has
	// to stay running (no restarts) to count as ready.
	stableRunningFor  = 5 * time.Second
	readyPollInterval = 1 * time.Second
)

// containerName is the deterministic name of one replica of a service.
// Replica 1 keeps the original single-container name, so stacks deployed
// before scaling existed are still found by DeployService/rolling updates.
func containerName(deploymentID, service string, replica int) string {
	if replica <= 1 {
		return "pe-" + deploymentID + "-" + service
	}
	return fmt.Sprintf("pe-%s-%s-%d", deploymentID, service, replica)
}

// ServiceOrder returns project's service names in dependency order: every
// service comes after everything it depends_on, ties broken alphabetically
// so the order is stable across deploys. A dependency on an undeclared
// service or a dependency cycle is an error — the control plane validates
// both before saving a Compose file, so this only fires for YAML that
// bypassed that (e.g. a catalog stack).
func ServiceOrder(project *types.Project) ([]string, error) {
	deps := make(map[string][]string, len(project.Services))
	for name, svc := range project.Services {
		for dep := range svc.DependsOn {
			if _, ok := project.Services[dep]; !ok {
				return nil, fmt.Errorf("service %q depends on undefined service %q", name, dep)
			}
			deps[name] = append(deps[name], dep)
		}
	}
	names := make([]string, 0, len(project.Services))
	for name := range project.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return topoSort(names, deps)
}

// topoSort orders names so each comes after its deps, visiting in the given
// order for stability.
func topoSort(names []string, deps map[string][]string) ([]string, error) {
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(names))
	order := make([]string, 0, len(names))
	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("dependency cycle: %s", strings.Join(append(path, name), " -> "))
		}
		state[name] = visiting
		sorted := append([]string(nil), deps[name]...)
		sort.Strings(sorted)
		for _, d := range sorted {
			if err := visit(d, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = done
		order = append(order, name)
		return nil
	}
	for _, n := range names {
		if err := visit(n, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// DesiredReplicas resolves how many containers svc should run: an explicit
// override from the control plane (scaling), else the Compose file's own
// deploy.replicas or scale, else 1.
func DesiredReplicas(svc types.ServiceConfig, override int32) int {
	if override > 0 {
		return int(override)
	}
	if svc.Deploy != nil && svc.Deploy.Replicas != nil && *svc.Deploy.Replicas > 0 {
		return *svc.Deploy.Replicas
	}
	if svc.Scale != nil && *svc.Scale > 0 {
		return *svc.Scale
	}
	return 1
}

// checkScalable rejects more than one replica of a service that publishes
// a fixed host port — every replica would try to bind the same port, and
// all but the first would fail to start.
func checkScalable(name string, svc types.ServiceConfig, replicas int) error {
	if replicas <= 1 {
		return nil
	}
	for _, p := range svc.Ports {
		if p.Published != "" && !strings.Contains(p.Published, "-") {
			return fmt.Errorf("service %q publishes host port %s, so it can't run more than one replica", name, p.Published)
		}
	}
	return nil
}

// ensureNetworks creates (or reuses) every network the project's services
// attach to and returns compose network name -> Docker network name. The
// implicit "default" network is the per-deployment "pe-<deployment_id>"
// network (ensureNetwork); other declared networks become
// "pe-<deployment_id>-<name>", and external ones are used as-is by name
// (they must already exist on the host).
func ensureNetworks(ctx context.Context, cli *client.Client, deploymentID, stackName string, project *types.Project) (map[string]string, error) {
	resolved := map[string]string{}
	needed := map[string]bool{"default": true}
	for _, svc := range project.Services {
		for name := range svc.Networks {
			needed[name] = true
		}
	}
	for name := range needed {
		cfg, declared := project.Networks[name]
		switch {
		case declared && bool(cfg.External):
			externalName := cfg.Name
			if externalName == "" {
				externalName = name
			}
			if _, err := cli.NetworkInspect(ctx, externalName, network.InspectOptions{}); err != nil {
				return nil, fmt.Errorf("external network %q not found on this host: %w", externalName, err)
			}
			resolved[name] = externalName
		case name == "default":
			n, err := ensureNetwork(ctx, cli, deploymentID, stackName)
			if err != nil {
				return nil, err
			}
			resolved[name] = n
		default:
			dockerName := "pe-" + deploymentID + "-" + name
			if err := ensureNamedNetwork(ctx, cli, dockerName, deploymentID, stackName, cfg); err != nil {
				return nil, fmt.Errorf("network %q: %w", name, err)
			}
			resolved[name] = dockerName
		}
	}
	return resolved, nil
}

func ensureNamedNetwork(ctx context.Context, cli *client.Client, dockerName, deploymentID, stackName string, cfg types.NetworkConfig) error {
	existing, err := cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(filters.Arg("name", dockerName))})
	if err != nil {
		return err
	}
	for _, n := range existing {
		if n.Name == dockerName {
			return nil
		}
	}
	driver := cfg.Driver
	if driver == "" {
		driver = "bridge"
	}
	labels := map[string]string{labelDeploymentID: deploymentID, labelStack: stackName}
	for k, v := range cfg.Labels {
		labels[k] = v
	}
	_, err = cli.NetworkCreate(ctx, dockerName, network.CreateOptions{
		Driver:     driver,
		Internal:   cfg.Internal,
		Attachable: cfg.Attachable,
		Options:    cfg.DriverOpts,
		Labels:     labels,
	})
	return err
}

// removeDeploymentNetworks removes every non-external network this agent
// created for deploymentID (the default "pe-<id>" one and any declared
// custom ones), found by label. A network still in use by something outside
// the deployment is left alone rather than failing the teardown.
func removeDeploymentNetworks(ctx context.Context, cli *client.Client, deploymentID string) error {
	nets, err := cli.NetworkList(ctx, network.ListOptions{Filters: filters.NewArgs(filters.Arg("label", labelDeploymentID+"="+deploymentID))})
	if err != nil {
		return err
	}
	for _, n := range nets {
		if err := cli.NetworkRemove(ctx, n.ID); err != nil && !errdefs.IsNotFound(err) && !errdefs.IsConflict(err) {
			return err
		}
	}
	return nil
}

// serviceNetworks returns the compose network names svc attaches to, in a
// stable order ("default" if it lists none).
func serviceNetworks(svc types.ServiceConfig) []string {
	if len(svc.Networks) == 0 {
		return []string{"default"}
	}
	names := make([]string, 0, len(svc.Networks))
	for n := range svc.Networks {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		pi, pj := networkPriority(svc, names[i]), networkPriority(svc, names[j])
		if pi != pj {
			return pi > pj
		}
		return names[i] < names[j]
	})
	return names
}

func networkPriority(svc types.ServiceConfig, name string) int {
	if cfg := svc.Networks[name]; cfg != nil {
		return cfg.Priority
	}
	return 0
}

func endpointFor(svc types.ServiceConfig, serviceName, composeNetwork string) *network.EndpointSettings {
	aliases := []string{serviceName}
	settings := &network.EndpointSettings{}
	if cfg := svc.Networks[composeNetwork]; cfg != nil {
		aliases = append(aliases, cfg.Aliases...)
		if cfg.Ipv4Address != "" || cfg.Ipv6Address != "" {
			settings.IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: cfg.Ipv4Address, IPv6Address: cfg.Ipv6Address}
		}
	}
	settings.Aliases = aliases
	return settings
}

// serviceContainer is one existing container of a deployment's service.
type serviceContainer struct {
	ID      string
	Name    string
	Service string
	Replica int
	Running bool
	// SetAside is true for a previous container a rolling update renamed
	// to "<name>-prev" and stopped while its replacement comes up. It's
	// not part of the live stack: dependency checks, verification, and
	// scaling must ignore it.
	SetAside bool
}

// setAsideSuffix marks a container a rolling update has set aside.
const setAsideSuffix = "-prev"

// liveContainers drops set-aside containers from list.
func liveContainers(list []serviceContainer) []serviceContainer {
	out := list[:0:0]
	for _, c := range list {
		if !c.SetAside {
			out = append(out, c)
		}
	}
	return out
}

// deploymentContainers lists every container labeled with deploymentID,
// resolving each one's service/replica from its labels, or — for
// containers created before those labels existed — from its name.
func deploymentContainers(ctx context.Context, cli *client.Client, deploymentID string) ([]serviceContainer, error) {
	f := filters.NewArgs(filters.Arg("label", labelDeploymentID+"="+deploymentID))
	list, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
	}
	prefix := "pe-" + deploymentID + "-"
	out := make([]serviceContainer, 0, len(list))
	for _, c := range list {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		sc := serviceContainer{ID: c.ID, Name: name, Service: c.Labels[labelService], Running: c.State == "running", Replica: 1, SetAside: strings.HasSuffix(name, setAsideSuffix)}
		if r, err := strconv.Atoi(c.Labels[labelReplica]); err == nil {
			sc.Replica = r
		}
		if sc.Service == "" {
			sc.Service = strings.TrimSuffix(strings.TrimPrefix(name, prefix), setAsideSuffix)
		}
		out = append(out, sc)
	}
	return out, nil
}

// createServiceContainer creates (but doesn't start) one replica of a
// service, attached to every network it declares.
func createServiceContainer(ctx context.Context, cli *client.Client, deploymentID, stackName, serviceName string, replica int, svc types.ServiceConfig, networks map[string]string, volumeNames map[string]string) (string, error) {
	nets := serviceNetworks(svc)
	primary, ok := networks[nets[0]]
	if !ok {
		return "", fmt.Errorf("service %q uses undeclared network %q", serviceName, nets[0])
	}
	id, err := createContainer(ctx, cli, deploymentID, stackName, serviceName, replica, svc, primary, endpointFor(svc, serviceName, nets[0]), volumeNames)
	if err != nil {
		return "", err
	}
	for _, n := range nets[1:] {
		dockerName, ok := networks[n]
		if !ok {
			_ = cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
			return "", fmt.Errorf("service %q uses undeclared network %q", serviceName, n)
		}
		if err := cli.NetworkConnect(ctx, dockerName, id, endpointFor(svc, serviceName, n)); err != nil {
			_ = cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
			return "", fmt.Errorf("failed to connect to network %q: %w", n, err)
		}
	}
	return id, nil
}

// waitForDependencies blocks until every depends_on condition of svc is
// met: service_healthy waits for the dependency's containers to report
// healthy, service_completed_successfully for them to exit 0, and
// service_started (the default) needs nothing beyond start order.
func waitForDependencies(ctx context.Context, cli *client.Client, deploymentID string, svc types.ServiceConfig) error {
	for dep, cfg := range svc.DependsOn {
		switch cfg.Condition {
		case types.ServiceConditionHealthy, types.ServiceConditionCompletedSuccessfully:
		default:
			continue
		}
		containers, err := deploymentContainers(ctx, cli, deploymentID)
		if err != nil {
			return err
		}
		for _, c := range liveContainers(containers) {
			if c.Service != dep {
				continue
			}
			var werr error
			if cfg.Condition == types.ServiceConditionHealthy {
				werr = waitReady(ctx, cli, c.ID, dependencyWaitTimeout, true)
			} else {
				werr = waitExitedSuccessfully(ctx, cli, c.ID, dependencyWaitTimeout)
			}
			if werr != nil {
				if !cfg.Required {
					continue
				}
				return fmt.Errorf("dependency %q (%s) not met: %w", dep, cfg.Condition, werr)
			}
		}
	}
	return nil
}

// waitReady waits for containerID to be ready: healthy if it has a
// healthcheck, otherwise running without restarts for stableRunningFor.
// requireHealth fails immediately for a container with no healthcheck
// instead of falling back to "stays running" (a service_healthy
// dependency on a service without one can never be satisfied).
func waitReady(ctx context.Context, cli *client.Client, containerID string, timeout time.Duration, requireHealth bool) error {
	deadline := time.Now().Add(timeout)
	var runningSince time.Time
	initialRestarts := -1
	for {
		info, err := cli.ContainerInspect(ctx, containerID)
		if err != nil {
			return err
		}
		state := info.State
		if state == nil {
			return errors.New("container has no state")
		}
		if initialRestarts < 0 {
			initialRestarts = info.RestartCount
		}
		if info.RestartCount-initialRestarts >= 2 {
			return fmt.Errorf("container is restarting repeatedly (%d restarts)", info.RestartCount)
		}
		switch {
		case state.Health != nil && state.Health.Status == container.Unhealthy:
			return fmt.Errorf("healthcheck failing%s", lastHealthOutput(state.Health))
		case state.Health != nil && state.Health.Status == container.Healthy:
			return nil
		case state.Status == "exited" || state.Status == "dead":
			return fmt.Errorf("container exited with code %d", state.ExitCode)
		case state.Health == nil && requireHealth:
			return errors.New("service has no healthcheck to wait on")
		case state.Health == nil && state.Running && !state.Restarting:
			if runningSince.IsZero() {
				runningSince = time.Now()
			}
			if time.Since(runningSince) >= stableRunningFor {
				return nil
			}
		default:
			runningSince = time.Time{}
		}
		if time.Now().After(deadline) {
			if state.Health != nil {
				return fmt.Errorf("timed out after %s waiting for healthcheck (status %q)", timeout, state.Health.Status)
			}
			return fmt.Errorf("timed out after %s waiting for container to stay running (state %q)", timeout, state.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPollInterval):
		}
	}
}

func waitExitedSuccessfully(ctx context.Context, cli *client.Client, containerID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		info, err := cli.ContainerInspect(ctx, containerID)
		if err != nil {
			return err
		}
		if info.State != nil && (info.State.Status == "exited" || info.State.Status == "dead") {
			if info.State.ExitCode == 0 {
				return nil
			}
			return fmt.Errorf("exited with code %d", info.State.ExitCode)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for it to complete", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPollInterval):
		}
	}
}

func lastHealthOutput(h *container.Health) string {
	if h == nil || len(h.Log) == 0 {
		return ""
	}
	out := strings.TrimSpace(h.Log[len(h.Log)-1].Output)
	if len(out) > 200 {
		out = out[:200] + "..."
	}
	if out == "" {
		return ""
	}
	return ": " + out
}

// isOneShot reports whether a container that exited 0 should count as
// done rather than failed: a service with no restart policy (a migration
// or init job) is expected to exit.
func isOneShot(svc types.ServiceConfig) bool {
	return svc.Restart == "" || svc.Restart == "no"
}

// VerifyDeployment is post-deployment health verification: waits (up to
// timeout, all containers in parallel) for every container of the
// deployment to be ready — see waitReady — and returns a per-service
// summary of anything that isn't. A one-shot service's container that
// exited 0 counts as healthy.
func VerifyDeployment(ctx context.Context, cli *client.Client, deploymentID string, project *types.Project, timeout time.Duration) error {
	all, err := deploymentContainers(ctx, cli, deploymentID)
	if err != nil {
		return err
	}
	containers := liveContainers(all)
	if len(containers) == 0 {
		return errors.New("no containers found for this deployment")
	}
	type result struct {
		c   serviceContainer
		err error
	}
	results := make(chan result, len(containers))
	for _, c := range containers {
		go func(c serviceContainer) {
			err := waitReady(ctx, cli, c.ID, timeout, false)
			if err != nil && strings.HasPrefix(err.Error(), "container exited with code 0") {
				if svc, ok := project.Services[c.Service]; ok && isOneShot(svc) {
					err = nil
				}
			}
			results <- result{c: c, err: err}
		}(c)
	}
	var problems []string
	for range containers {
		r := <-results
		if r.err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", r.c.Name, r.err))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
