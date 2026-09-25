package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/compose"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/registryclient"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

type deploymentStatusResponse struct {
	store.Deployment
	Events []store.DeploymentEvent `json:"events"`
}

// triggeredByFromContext returns the authenticated user's ID for
// AddDeploymentEvent's triggeredBy, or "" if there's none — every route
// that calls this is already behind RequireAuth, so the empty case is
// defensive rather than expected.
func triggeredByFromContext(ctx context.Context) string {
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		return claims.UserID
	}
	return ""
}

// deploymentPreviewService summarizes one service compose-go resolved out
// of a compose file (after env-var substitution), for the "preview
// resources before deployment" step — what would be created. NanoCPUs/
// MemoryLimitBytes are 0 when the service declares no deploy.resources
// limit, i.e. an unbounded request the resource check below can't weigh.
type deploymentPreviewService struct {
	Name             string   `json:"name"`
	Image            string   `json:"image"`
	Ports            []string `json:"ports,omitempty"`
	Volumes          []string `json:"volumes,omitempty"`
	EnvironmentCount int      `json:"environmentCount"`
	NanoCPUs         int64    `json:"nanoCpus,omitempty"`
	MemoryLimitBytes int64    `json:"memoryLimitBytes,omitempty"`
}

// deploymentResourceCheck is "Confirm sufficient CPU, RAM, and storage" +
// "Show estimated resource consumption" + a simple "Generate deployment
// risk score", computed only when the request names a target server that
// has reported host capacity (an agent older than the total_memory_bytes/
// num_cpus/total_disk_bytes heartbeat fields, or one that hasn't
// heartbeated yet, reports 0 = unknown — in which case this whole section
// is omitted from the response rather than comparing against a false
// zero).
type deploymentResourceCheck struct {
	RequestedNanoCPUs          int64   `json:"requestedNanoCpus"`
	RequestedMemoryBytes       int64   `json:"requestedMemoryBytes"`
	ServerTotalCPUs            uint32  `json:"serverTotalCpus"`
	ServerTotalMemoryBytes     uint64  `json:"serverTotalMemoryBytes"`
	ServerAvailableMemoryBytes int64   `json:"serverAvailableMemoryBytes"`
	ServerAvailableCPUCores    float64 `json:"serverAvailableCpuCores"`
	ServerDiskPercentUsed      float64 `json:"serverDiskPercentUsed"`
	SufficientMemory           bool    `json:"sufficientMemory"`
	SufficientCPU              bool    `json:"sufficientCpu"`
	// RiskScore is "low", "medium", or "high" — a simple heuristic, not a
	// substitute for actually watching the deployment after it's up.
	RiskScore string `json:"riskScore"`
}

// deploymentPortConflict is "Check required ports": one service's
// published host port that's already bound by another container on the
// target server, found via the cached inventory (store.FindPortConflict's
// same no-agent-round-trip lookup) — not a guarantee nothing else on the
// host holds it, just what this control plane already knows about.
type deploymentPortConflict struct {
	Service     string `json:"service"`
	HostPort    int    `json:"hostPort"`
	Protocol    string `json:"protocol"`
	ContainerID string `json:"containerId"`
}

// deploymentImageCheck is "Check image availability" + "Check host
// architecture compatibility" together, since both need the same registry
// lookup. Available=false means the image couldn't be resolved at all
// (not found, or not reachable with anonymous credentials — see
// checkImages' doc comment on why only anonymous access is tried).
// ArchCompatible is meaningless when Available is false.
type deploymentImageCheck struct {
	Service        string   `json:"service"`
	Image          string   `json:"image"`
	Available      bool     `json:"available"`
	Error          string   `json:"error,omitempty"`
	Platforms      []string `json:"platforms,omitempty"`
	ArchCompatible bool     `json:"archCompatible"`
}

// deploymentVolumeWarning is "Validate volume paths": a service's named
// volume with an empty, non-absolute, or duplicated mount target.
type deploymentVolumeWarning struct {
	Service string `json:"service"`
	Target  string `json:"target"`
	Message string `json:"message"`
}

type deploymentPreviewResponse struct {
	Name            string                     `json:"name"`
	Services        []deploymentPreviewService `json:"services"`
	ResourceCheck   *deploymentResourceCheck   `json:"resourceCheck,omitempty"`
	PortConflicts   []deploymentPortConflict   `json:"portConflicts,omitempty"`
	ImageChecks     []deploymentImageCheck     `json:"imageChecks,omitempty"`
	VolumeWarnings  []deploymentVolumeWarning  `json:"volumeWarnings,omitempty"`
	MissingSecrets  []string                   `json:"missingSecrets,omitempty"`
	NetworkWarnings []string                   `json:"networkWarnings,omitempty"`
}

// requestedPort is one service's published host port, gathered while
// building the preview so checkDeploymentPortConflicts doesn't have to re-parse the
// display strings in deploymentPreviewService.Ports.
type requestedPort struct {
	service  string
	hostPort uint16
	protocol string
}

// handlePreviewDeployment resolves a stack or compose file's services
// (same loader compose-go path composeImages uses for the image-policy
// check) without creating anything, so the deploy dialog can show exactly
// what would be created — images, ports, volumes, declared resource
// limits — and, when serverId is given, whether the target server
// currently has room for the declared CPU/memory requests.
func handlePreviewDeployment(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		StackID       string            `json:"stackId"`
		ComposeFileID string            `json:"composeFileId"`
		ServerID      string            `json:"serverId,omitempty"`
		Env           map[string]string `json:"env"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if (req.StackID == "") == (req.ComposeFileID == "") {
			http.Error(w, "exactly one of stackId/composeFileId is required", http.StatusBadRequest)
			return
		}

		env := req.Env
		if env == nil {
			env = map[string]string{}
		}

		var name, composeYAML string
		if req.StackID != "" {
			stack, err := st.GetStack(r.Context(), req.StackID)
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "stack not found", http.StatusNotFound)
				return
			}
			if err != nil {
				log.Error("failed to load stack", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			merged := map[string]string{}
			for k, v := range stack.DefaultEnv {
				merged[k] = v
			}
			for k, v := range env {
				merged[k] = v
			}
			env = merged
			name, composeYAML = stack.Name, stack.ComposeYAML
		} else {
			file, err := st.GetComposeFile(r.Context(), req.ComposeFileID)
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "compose file not found", http.StatusNotFound)
				return
			}
			if err != nil {
				log.Error("failed to load compose file", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			name, composeYAML = file.Name, file.Content
		}

		details := types.ConfigDetails{
			ConfigFiles: []types.ConfigFile{{Filename: "compose.yaml", Content: []byte(composeYAML)}},
			Environment: env,
		}
		project, err := loader.LoadWithContext(r.Context(), details, func(o *loader.Options) {
			o.SetProjectName("preview", true)
			o.SkipConsistencyCheck = true
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to parse compose file: " + err.Error()})
			return
		}

		services := make([]deploymentPreviewService, 0, len(project.Services))
		var requestedPorts []requestedPort
		for svcName, svc := range project.Services {
			preview := deploymentPreviewService{
				Name:             svcName,
				Image:            svc.Image,
				EnvironmentCount: len(svc.Environment),
			}
			for _, p := range svc.Ports {
				protocol := p.Protocol
				if protocol == "" {
					protocol = "tcp"
				}
				if p.Published != "" {
					preview.Ports = append(preview.Ports, fmt.Sprintf("%s:%d/%s", p.Published, p.Target, protocol))
					if hostPort, err := strconv.ParseUint(p.Published, 10, 16); err == nil {
						requestedPorts = append(requestedPorts, requestedPort{
							service: svcName, hostPort: uint16(hostPort), protocol: protocol,
						})
					}
				} else {
					preview.Ports = append(preview.Ports, fmt.Sprintf("%d/%s", p.Target, protocol))
				}
			}
			for _, v := range svc.Volumes {
				entry := v.Source + ":" + v.Target
				if v.ReadOnly {
					entry += ":ro"
				}
				preview.Volumes = append(preview.Volumes, entry)
			}
			if svc.Deploy != nil {
				if limits := svc.Deploy.Resources.Limits; limits != nil {
					preview.NanoCPUs = int64(float64(limits.NanoCPUs) * 1e9)
					preview.MemoryLimitBytes = int64(limits.MemoryBytes)
				}
			}
			sort.Strings(preview.Ports)
			sort.Strings(preview.Volumes)
			services = append(services, preview)
		}
		sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

		var resourceCheck *deploymentResourceCheck
		var portConflicts []deploymentPortConflict
		var imageChecks []deploymentImageCheck
		if req.ServerID != "" {
			resourceCheck = buildResourceCheck(r.Context(), log, st, req.ServerID, services)
			portConflicts = checkDeploymentPortConflicts(r.Context(), log, st, req.ServerID, requestedPorts)

			var serverArch string
			if server, err := st.GetServer(r.Context(), req.ServerID); err == nil {
				serverArch = server.Arch
			} else if !errors.Is(err, store.ErrNotFound) {
				log.Error("failed to load server for image check", "error", err)
			}
			imageChecks = checkImages(r.Context(), services, serverArch)
		}

		writeJSON(w, http.StatusOK, deploymentPreviewResponse{
			Name:            name,
			Services:        services,
			ResourceCheck:   resourceCheck,
			PortConflicts:   portConflicts,
			ImageChecks:     imageChecks,
			VolumeWarnings:  validateVolumePaths(services),
			MissingSecrets:  detectMissingSecrets(composeYAML, env),
			NetworkWarnings: validateNetworkConfiguration(project),
		})
	}
}

// imageCheckTimeout bounds all of checkImages' registry lookups together
// — a slow or unreachable registry must not hang the whole preview
// request indefinitely.
const imageCheckTimeout = 15 * time.Second

// checkImages is "Check image availability" + "Check host architecture
// compatibility". Looks up each distinct image (not each service — a
// compose file commonly repeats the same base image across services, and
// there's no point resolving it twice) concurrently via
// registryclient.Platforms, using anonymous credentials only: this
// codebase has no way to match an arbitrary image reference to one of the
// configured private registries (see resolveCreds, which always requires
// an explicit registryId), so a private image without anonymous pull
// access reports unavailable even though it would work once deployed.
func checkImages(ctx context.Context, services []deploymentPreviewService, serverArch string) []deploymentImageCheck {
	ctx, cancel := context.WithTimeout(ctx, imageCheckTimeout)
	defer cancel()

	type lookup struct {
		platforms []string
		err       error
	}
	distinct := map[string]bool{}
	for _, s := range services {
		distinct[s.Image] = true
	}

	results := make(map[string]lookup, len(distinct))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for image := range distinct {
		wg.Add(1)
		go func(image string) {
			defer wg.Done()
			platforms, err := registryclient.Platforms(ctx, image, registryclient.Credentials{})
			mu.Lock()
			results[image] = lookup{platforms: platforms, err: err}
			mu.Unlock()
		}(image)
	}
	wg.Wait()

	checks := make([]deploymentImageCheck, 0, len(services))
	for _, s := range services {
		r := results[s.Image]
		check := deploymentImageCheck{Service: s.Name, Image: s.Image}
		if r.err != nil {
			check.Error = r.err.Error()
		} else {
			check.Available = true
			check.Platforms = r.platforms
			check.ArchCompatible = serverArch == "" || len(r.platforms) == 0 || containsString(r.platforms, serverArch)
		}
		checks = append(checks, check)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Service < checks[j].Service })
	return checks
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// validateVolumePaths is "Validate volume paths": flags an empty or
// non-absolute mount target, or a target reused by more than one volume
// on the same service (they can never both actually be mounted).
func validateVolumePaths(services []deploymentPreviewService) []deploymentVolumeWarning {
	var warnings []deploymentVolumeWarning
	for _, svc := range services {
		seen := map[string]bool{}
		for _, v := range svc.Volumes {
			parts := strings.SplitN(v, ":", 3)
			if len(parts) < 2 {
				continue
			}
			target := parts[1]
			switch {
			case target == "":
				warnings = append(warnings, deploymentVolumeWarning{Service: svc.Name, Target: target, Message: "volume has no mount path"})
			case !strings.HasPrefix(target, "/"):
				warnings = append(warnings, deploymentVolumeWarning{Service: svc.Name, Target: target, Message: "mount path must be absolute"})
			case seen[target]:
				warnings = append(warnings, deploymentVolumeWarning{Service: svc.Name, Target: target, Message: "mount path is used by more than one volume on this service"})
			default:
				seen[target] = true
			}
		}
	}
	return warnings
}

// envVarReferencePattern matches Compose's ${VAR}, ${VAR:-default} /
// ${VAR-default} / ${VAR:?msg}, and bare $VAR interpolation forms.
var envVarReferencePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)([:?+-][^}]*)?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// detectMissingSecrets is "Detect missing secrets": scans the raw Compose
// YAML for interpolation references with no fallback value that also
// aren't present in the deploy-time env — compose-go silently substitutes
// those with an empty string rather than failing, which is exactly the
// "forgot to set DB_PASSWORD" mistake this exists to catch before
// deploying, not after. Best-effort: a ${...} appearing inside an
// unrelated string value (e.g. an embedded config template) would be a
// false positive, an accepted tradeoff for not needing a real templating
// parser here.
func detectMissingSecrets(composeYAML string, env map[string]string) []string {
	seen := map[string]bool{}
	var missing []string
	for _, m := range envVarReferencePattern.FindAllStringSubmatch(composeYAML, -1) {
		name := m[1]
		hasFallback := m[2] != ""
		if name == "" {
			name = m[3]
		}
		if name == "" || hasFallback || seen[name] {
			continue
		}
		if _, ok := env[name]; ok {
			continue
		}
		seen[name] = true
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return missing
}

// supportedNetworkDrivers are the network drivers a standalone (non-Swarm)
// Docker host can create for a deployment.
var supportedNetworkDrivers = map[string]bool{"": true, "bridge": true, "macvlan": true, "ipvlan": true}

// validateNetworkConfiguration is "Validate network configuration": the
// deploy engine creates every declared network per deployment (see
// agent/docker.ensureNetworks) and attaches services to the ones they list,
// so this flags only what it can't honor: drivers that need Swarm (overlay)
// or aren't available standalone, IPAM settings (ignored — Docker assigns
// subnets), external networks (which must already exist on the target
// host), services referencing undeclared networks, and network_mode.
func validateNetworkConfiguration(project *types.Project) []string {
	var warnings []string
	for name, net := range project.Networks {
		switch {
		case bool(net.External):
			warnings = append(warnings, fmt.Sprintf("network %q is external — it must already exist on the target server", name))
		case !supportedNetworkDrivers[net.Driver]:
			warnings = append(warnings, fmt.Sprintf("network %q uses driver %q, which isn't supported on a standalone Docker host (use bridge)", name, net.Driver))
		case len(net.Ipam.Config) > 0 || net.Ipam.Driver != "":
			warnings = append(warnings, fmt.Sprintf("network %q sets IPAM options, which are ignored — Docker assigns the subnet", name))
		}
	}
	for svcName, svc := range project.Services {
		if svc.NetworkMode != "" {
			warnings = append(warnings, fmt.Sprintf("service %q sets network_mode %q, which isn't supported — it joins the deployment's networks instead", svcName, svc.NetworkMode))
		}
		for netName := range svc.Networks {
			if _, ok := project.Networks[netName]; !ok && netName != "default" {
				warnings = append(warnings, fmt.Sprintf("service %q references network %q, which isn't declared", svcName, netName))
			}
		}
	}
	sort.Strings(warnings)
	return warnings
}

// checkDeploymentPortConflicts is "Check required ports": for each service's
// published host port, whether another container on serverID already
// binds it — one ListContainersFiltered call regardless of how many ports
// are being checked, rather than one store.FindPortConflict call per port
// (which would each re-fetch the whole container list).
func checkDeploymentPortConflicts(ctx context.Context, log *slog.Logger, st *store.Store, serverID string, requests []requestedPort) []deploymentPortConflict {
	if len(requests) == 0 {
		return nil
	}
	containers, err := st.ListContainersFiltered(ctx, store.ContainerFilter{ServerID: serverID})
	if err != nil {
		log.Error("failed to list containers for port check", "error", err)
		return nil
	}
	return matchPortConflicts(requests, containers)
}

// matchPortConflicts is checkDeploymentPortConflicts' pure matching logic
// — factored out so it's unit-testable without a database, same reasoning
// as computeResourceCheck's split from buildResourceCheck.
func matchPortConflicts(requests []requestedPort, containers []store.FleetContainer) []deploymentPortConflict {
	var conflicts []deploymentPortConflict
	for _, req := range requests {
		for _, c := range containers {
			for _, p := range c.Ports {
				if p.PublicPort == req.hostPort && p.Type == req.protocol {
					conflicts = append(conflicts, deploymentPortConflict{
						Service:     req.service,
						HostPort:    int(req.hostPort),
						Protocol:    req.protocol,
						ContainerID: c.ContainerID,
					})
				}
			}
		}
	}
	return conflicts
}

// buildResourceCheck computes deploymentResourceCheck for services against
// serverID's last-reported capacity, or nil if that server hasn't reported
// capacity yet (see deploymentResourceCheck's doc comment) or can't be
// loaded. A lookup failure here degrades to "no resource check" rather
// than failing the whole preview — the topology/image/port information
// above is still useful on its own.
func buildResourceCheck(ctx context.Context, log *slog.Logger, st *store.Store, serverID string, services []deploymentPreviewService) *deploymentResourceCheck {
	server, err := st.GetServer(ctx, serverID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Error("failed to load server for resource check", "error", err)
		}
		return nil
	}
	if len(server.LastResources) == 0 {
		return nil
	}
	var resources store.ResourceSnapshot
	if err := json.Unmarshal(server.LastResources, &resources); err != nil || resources.TotalMemoryBytes == 0 {
		return nil
	}
	return computeResourceCheck(resources, services)
}

// computeResourceCheck is buildResourceCheck's pure heuristic — factored
// out so it's unit-testable without a database, the same reasoning as
// matchesImagePattern's split from IsImageApproved (see images.go).
func computeResourceCheck(resources store.ResourceSnapshot, services []deploymentPreviewService) *deploymentResourceCheck {
	var requestedCPUs, requestedMem int64
	for _, s := range services {
		requestedCPUs += s.NanoCPUs
		requestedMem += s.MemoryLimitBytes
	}

	availableMem := int64(float64(resources.TotalMemoryBytes) * (1 - resources.MemPercent/100))
	availableCPUs := float64(resources.NumCPUs) * (1 - resources.CPUPercent/100)

	check := &deploymentResourceCheck{
		RequestedNanoCPUs:          requestedCPUs,
		RequestedMemoryBytes:       requestedMem,
		ServerTotalCPUs:            resources.NumCPUs,
		ServerTotalMemoryBytes:     resources.TotalMemoryBytes,
		ServerAvailableMemoryBytes: availableMem,
		ServerAvailableCPUCores:    availableCPUs,
		ServerDiskPercentUsed:      resources.DiskPercent,
	}
	check.SufficientMemory = requestedMem == 0 || requestedMem <= availableMem
	check.SufficientCPU = requestedCPUs == 0 || float64(requestedCPUs)/1e9 <= availableCPUs

	switch {
	case !check.SufficientMemory || !check.SufficientCPU:
		check.RiskScore = "high"
	case resources.DiskPercent > 85:
		check.RiskScore = "medium"
	case requestedMem > 0 && float64(availableMem) < float64(requestedMem)*1.25:
		check.RiskScore = "medium"
	default:
		check.RiskScore = "low"
	}
	return check
}

// deploymentGovernanceFields are the metadata fields shared by create,
// promote, and metadata updates.
type deploymentGovernanceFields struct {
	Environment    *string  `json:"environment,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	ChangeRequest  string   `json:"changeRequest,omitempty"`
	Notes          string   `json:"notes,omitempty"`
	RollbackPlan   string   `json:"rollbackPlan,omitempty"`
	AutoRollback   bool     `json:"autoRollback,omitempty"`
	UpdateStrategy string   `json:"updateStrategy,omitempty"`
	AutoDeploy     bool     `json:"autoDeploy,omitempty"`
	GitRef         string   `json:"gitRef,omitempty"`
}

func (f deploymentGovernanceFields) validate() error {
	if f.Environment != nil && *f.Environment != "" && !store.IsValidEnvironment(*f.Environment) {
		return newActionError(http.StatusBadRequest, "environment must be one of development, test, staging, production")
	}
	if f.UpdateStrategy != "" && f.UpdateStrategy != "recreate" && f.UpdateStrategy != "rolling" {
		return newActionError(http.StatusBadRequest, "updateStrategy must be recreate or rolling")
	}
	return nil
}

func (f deploymentGovernanceFields) environment() *string {
	if f.Environment == nil || *f.Environment == "" {
		return nil
	}
	return f.Environment
}

// handleCreateDeployment creates a deployment from the stacks catalog or a
// Compose file and submits its first deploy through the governance gate —
// it may run now, wait for approval, or wait for a maintenance window (see
// deployer.submit). The response is 202 in every case; its "status" says
// which.
func handleCreateDeployment(d *deployer) http.HandlerFunc {
	type request struct {
		deploymentGovernanceFields
		gateOptions
		StackID       string            `json:"stackId"`
		ComposeFileID string            `json:"composeFileId"`
		ServerID      string            `json:"serverId"`
		Env           map[string]string `json:"env"`
		Scales        map[string]int    `json:"scales,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		if a.ID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.ServerID == "" || (req.StackID == "") == (req.ComposeFileID == "") {
			http.Error(w, "serverId and exactly one of stackId/composeFileId are required", http.StatusBadRequest)
			return
		}
		if err := req.validate(); err != nil {
			writeActionError(w, d.log, err)
			return
		}

		env := map[string]string{}
		n := store.NewDeployment{
			ServerID:       req.ServerID,
			CreatedBy:      a.ID,
			Environment:    req.environment(),
			Tags:           req.Tags,
			ChangeRequest:  req.ChangeRequest,
			Notes:          req.Notes,
			RollbackPlan:   req.RollbackPlan,
			AutoRollback:   req.AutoRollback,
			Scales:         req.Scales,
			UpdateStrategy: req.UpdateStrategy,
			GitRef:         req.GitRef,
			AutoDeploy:     req.AutoDeploy,
		}
		var summary string
		if req.StackID != "" {
			stack, err := d.st.GetStack(r.Context(), req.StackID)
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "stack not found", http.StatusNotFound)
				return
			}
			if err != nil {
				writeActionError(w, d.log, err)
				return
			}
			for k, v := range stack.DefaultEnv {
				env[k] = v
			}
			n.StackID = &stack.ID
			summary = "deployment of stack " + stack.Name + " created"
		} else {
			file, err := d.st.GetComposeFile(r.Context(), req.ComposeFileID)
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "compose file not found", http.StatusNotFound)
				return
			}
			if err != nil {
				writeActionError(w, d.log, err)
				return
			}
			if req.GitRef != "" && file.GitRepositoryID == nil {
				http.Error(w, "gitRef is only valid for a Git-linked Compose file", http.StatusBadRequest)
				return
			}
			n.ComposeFileID = &file.ID
			summary = "deployment of compose file " + file.Name + " created"
		}
		for k, v := range req.Env {
			env[k] = v
		}
		n.Env = env

		outcome, deploymentID, err := d.create(r.Context(), a, n, req.gateOptions, summary)
		if err != nil {
			var ae *actionError
			if deploymentID != "" && errors.As(err, &ae) {
				if ae.extra == nil {
					ae.extra = map[string]any{}
				}
				ae.extra["deploymentId"] = deploymentID
			}
			writeActionError(w, d.log, err)
			return
		}
		writeJSON(w, http.StatusAccepted, outcome)
	}
}

// create checks n's environment policy, inserts the deployment, and
// submits its first deploy through the governance gate. deploymentID is
// set whenever the row was inserted, even if submitting then failed (e.g.
// the server isn't connected) — the deployment exists and is marked
// failed, and the caller should say so.
func (d *deployer) create(ctx context.Context, a actor, n store.NewDeployment, gate gateOptions, summary string) (outcome *actionOutcome, deploymentID string, err error) {
	// Fail fast on policy requirements before creating anything.
	env0 := ""
	if n.Environment != nil {
		env0 = *n.Environment
	}
	policy, err := d.st.GetEnvironmentPolicy(ctx, env0)
	if err != nil {
		return nil, "", err
	}
	if err := checkPolicyRequirements(policy, actionDeploy, n.ChangeRequest, n.RollbackPlan); err != nil {
		return nil, "", err
	}
	if err := d.precheckMaintenanceWindow(policy, a, gate); err != nil {
		return nil, "", err
	}

	deploymentID, err = d.st.InsertDeployment(ctx, n)
	if err != nil {
		return nil, "", err
	}
	d.event(ctx, deploymentID, "pending", "deployment created", a.ID)
	d.audit(ctx, a, "deployment.create", "deployment", deploymentID, summary, map[string]any{
		"serverId": n.ServerID, "environment": n.Environment, "changeRequest": n.ChangeRequest,
	})

	dep, err := d.st.GetDeployment(ctx, deploymentID)
	if err != nil {
		return nil, deploymentID, err
	}
	outcome, err = d.submit(ctx, dep, actionDeploy, actionParams{}, a, gate, true)
	if err != nil {
		var ae *actionError
		if errors.As(err, &ae) && ae.status != http.StatusConflict {
			_ = d.st.UpdateDeploymentPhase(ctx, deploymentID, "failed")
			d.event(ctx, deploymentID, "failed", ae.msg, a.ID)
		}
		return nil, deploymentID, err
	}
	return outcome, deploymentID, nil
}

// deploymentDetailResponse is GET /api/deployments/{id}: the deployment,
// its event timeline, open approval/scheduled requests, its environment's
// policy, and the revision a rollback would currently go back to.
type deploymentDetailResponse struct {
	store.Deployment
	Events         []store.DeploymentEvent   `json:"events"`
	OpenRequests   []store.DeploymentRequest `json:"openRequests"`
	Policy         *store.EnvironmentPolicy  `json:"policy,omitempty"`
	RollbackTarget *store.DeploymentRevision `json:"rollbackTarget,omitempty"`
	CurrentRev     *store.DeploymentRevision `json:"currentRevisionDetail,omitempty"`
	SourceName     string                    `json:"sourceName"`
	ServiceNames   []string                  `json:"serviceNames"`
}

func handleGetDeployment(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		ctx := r.Context()

		deployment, err := d.st.GetDeployment(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "deployment not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		events, err := d.st.ListDeploymentEvents(ctx, id)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		resp := deploymentDetailResponse{Deployment: *deployment, Events: events, OpenRequests: []store.DeploymentRequest{}, ServiceNames: []string{}}
		for _, status := range []string{store.RequestPendingApproval, store.RequestScheduled} {
			reqs, err := d.st.ListDeploymentRequests(ctx, status, id)
			if err != nil {
				writeActionError(w, d.log, err)
				return
			}
			resp.OpenRequests = append(resp.OpenRequests, reqs...)
		}
		if resp.Policy, err = d.st.GetEnvironmentPolicy(ctx, deploymentEnvironment(deployment)); err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if deployment.CurrentRevision > 0 {
			if cur, err := d.st.GetDeploymentRevision(ctx, id, deployment.CurrentRevision); err == nil {
				resp.ServiceNames = compose.Parse(cur.ComposeContent).ServiceNames
				cur.ComposeContent, cur.Env = "", nil
				resp.CurrentRev = cur
			}
			if target, err := d.st.LastGoodRevision(ctx, id, deployment.CurrentRevision); err == nil {
				target.ComposeContent, target.Env = "", nil
				resp.RollbackTarget = target
			}
		}
		if name, content, err := d.st.ResolveDeploymentSource(ctx, deployment); err == nil {
			resp.SourceName = name
			if len(resp.ServiceNames) == 0 {
				resp.ServiceNames = compose.Parse(content).ServiceNames
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleListDeployments is "Deployment history": GET /api/deployments with
// optional ?environment=, ?serverId=, ?composeFileId=, ?phase=, ?q=,
// ?includeRemoved=true, ?limit=.
func handleListDeployments(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		list, err := d.st.ListDeployments(r.Context(), store.DeploymentFilter{
			Environment:    q.Get("environment"),
			ServerID:       q.Get("serverId"),
			ComposeFileID:  q.Get("composeFileId"),
			Phase:          q.Get("phase"),
			Search:         q.Get("q"),
			IncludeRemoved: q.Get("includeRemoved") == "true",
			Limit:          limit,
		})
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// handleListDeploymentRevisions lists a deployment's rollouts, newest first.
func handleListDeploymentRevisions(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		revs, err := d.st.ListDeploymentRevisions(r.Context(), r.PathValue("id"))
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		writeJSON(w, http.StatusOK, revs)
	}
}

// handleGetDeploymentRevision returns one revision including the exact
// Compose content it ran (for viewing/diffing).
func handleGetDeploymentRevision(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("revision"))
		if err != nil {
			http.Error(w, "invalid revision", http.StatusBadRequest)
			return
		}
		rev, err := d.st.GetDeploymentRevision(r.Context(), r.PathValue("id"), n)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "revision not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		writeJSON(w, http.StatusOK, rev)
	}
}

// loadDeploymentOr404 loads the {id} deployment, writing the error response
// itself when it can't.
func (d *deployer) loadDeploymentOr404(w http.ResponseWriter, r *http.Request) (*store.Deployment, bool) {
	dep, err := d.st.GetDeployment(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "deployment not found", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		writeActionError(w, d.log, err)
		return nil, false
	}
	return dep, true
}

// submitAndRespond runs an action on the {id} deployment through the gate
// and writes the outcome — 202 when dispatched or queued, 200 when a
// synchronous action (scale, service redeploy) completed.
func (d *deployer) submitAndRespond(w http.ResponseWriter, r *http.Request, dep *store.Deployment, action string, p actionParams, opts gateOptions) {
	outcome, err := d.submit(r.Context(), dep, action, p, actorFromRequest(r), opts, false)
	if err != nil {
		writeActionError(w, d.log, err)
		return
	}
	status := http.StatusAccepted
	if outcome.Status == "completed" {
		status = http.StatusOK
	}
	writeJSON(w, status, outcome)
}

// decodeOptionalBody decodes a JSON body into v, treating an empty body as
// all-defaults (these endpoints were originally body-less POSTs).
func decodeOptionalBody(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// handleRedeployDeployment re-sends an existing deployment's stack to its
// server, keeping the same deployment_id. This is what actually exercises
// the agent's redeploy logic (recreate, or a rolling update with automatic
// rollback when the deployment's strategy is "rolling"): a fresh POST
// /api/deployments always mints a new deployment_id, so it can never
// collide with a previous run's labeled containers — only re-sending the
// *same* deployment_id does that.
func handleRedeployDeployment(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dep, ok := d.loadDeploymentOr404(w, r)
		if !ok {
			return
		}
		var opts gateOptions
		if err := decodeOptionalBody(r, &opts); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		d.submitAndRespond(w, r, dep, actionRedeploy, actionParams{}, opts)
	}
}

// handleRollbackDeployment is "Perform a controlled rollback": redeploys
// the whole stack with an earlier definition — a previous revision (what
// actually ran before, including its env and replica counts), a past
// Compose file version, or a past Git commit — as a one-time rollout, not
// a change to the Compose file itself, so it doesn't affect any other
// deployment sourced from the same file.
func handleRollbackDeployment(d *deployer) http.HandlerFunc {
	type request struct {
		gateOptions
		Revision  int    `json:"revision,omitempty"`
		VersionID string `json:"versionId,omitempty"`
		GitCommit string `json:"gitCommit,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		dep, ok := d.loadDeploymentOr404(w, r)
		if !ok {
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		set := 0
		for _, b := range []bool{req.Revision > 0, req.VersionID != "", req.GitCommit != ""} {
			if b {
				set++
			}
		}
		if set != 1 {
			http.Error(w, "exactly one of revision, versionId, gitCommit is required", http.StatusBadRequest)
			return
		}
		d.submitAndRespond(w, r, dep, actionRollback, actionParams{Revision: req.Revision, VersionID: req.VersionID, GitCommit: req.GitCommit}, req.gateOptions)
	}
}

// handleRedeployService redeploys a single named service within a
// deployment (pull its image, recreate just that service's containers),
// leaving every other service running untouched — "Redeploy an individual
// service", the finer-grained counterpart to handleRedeployDeployment.
func handleRedeployService(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dep, ok := d.loadDeploymentOr404(w, r)
		if !ok {
			return
		}
		var opts gateOptions
		if err := decodeOptionalBody(r, &opts); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		d.submitAndRespond(w, r, dep, actionRedeployService, actionParams{Service: r.PathValue("service")}, opts)
	}
}

// handleScaleService is "Scale supported services": sets how many
// containers one service runs, adding or removing replicas without
// touching the ones already running. The count is remembered on the
// deployment, so later redeploys keep it.
func handleScaleService(d *deployer) http.HandlerFunc {
	type request struct {
		gateOptions
		Replicas int `json:"replicas"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		dep, ok := d.loadDeploymentOr404(w, r)
		if !ok {
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		// Validate before possibly queueing it for approval.
		_, content, env, err := d.currentContent(r.Context(), dep)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		project, err := loadProject(r.Context(), content, env)
		if err != nil {
			http.Error(w, "failed to parse compose file: "+err.Error(), http.StatusBadRequest)
			return
		}
		service := r.PathValue("service")
		if err := validateScale(project, service, req.Replicas); err != nil {
			writeActionError(w, d.log, err)
			return
		}
		d.submitAndRespond(w, r, dep, actionScale, actionParams{Service: service, Replicas: req.Replicas}, req.gateOptions)
	}
}

// handlePromoteDeployment promotes a deployment to another environment
// (e.g. staging -> production): creates a new deployment on the target
// server that runs exactly what the source's current revision runs — same
// Compose content, Git commit, and replica counts — and submits it through
// the *target* environment's policy (so promoting into production goes
// through production's approval and maintenance window).
func handlePromoteDeployment(d *deployer) http.HandlerFunc {
	type request struct {
		deploymentGovernanceFields
		gateOptions
		ServerID string            `json:"serverId"`
		Env      map[string]string `json:"env,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		src, ok := d.loadDeploymentOr404(w, r)
		if !ok {
			return
		}
		a := actorFromRequest(r)
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.ServerID == "" || req.environment() == nil {
			http.Error(w, "serverId and environment are required", http.StatusBadRequest)
			return
		}
		if err := req.validate(); err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if src.CurrentRevision == 0 {
			http.Error(w, "the source deployment hasn't been deployed yet", http.StatusConflict)
			return
		}
		policy, err := d.st.GetEnvironmentPolicy(r.Context(), *req.environment())
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if err := checkPolicyRequirements(policy, actionDeploy, req.ChangeRequest, req.RollbackPlan); err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if err := d.precheckMaintenanceWindow(policy, a, req.gateOptions); err != nil {
			writeActionError(w, d.log, err)
			return
		}
		env := req.Env
		if env == nil {
			env = src.Env
		}
		strategy := req.UpdateStrategy
		if strategy == "" {
			strategy = src.UpdateStrategy
		}
		srcID := src.ID
		id, err := d.st.InsertDeployment(r.Context(), store.NewDeployment{
			StackID:        src.StackID,
			ComposeFileID:  src.ComposeFileID,
			ServerID:       req.ServerID,
			Env:            env,
			CreatedBy:      a.ID,
			Environment:    req.environment(),
			Tags:           req.Tags,
			ChangeRequest:  req.ChangeRequest,
			Notes:          req.Notes,
			RollbackPlan:   req.RollbackPlan,
			AutoRollback:   req.AutoRollback,
			Scales:         src.Scales,
			UpdateStrategy: strategy,
			GitRef:         src.GitRef,
			PromotedFrom:   &srcID,
		})
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		fromEnv := deploymentEnvironment(src)
		if fromEnv == "" {
			fromEnv = "no environment"
		}
		message := fmt.Sprintf("promoted from deployment %s (%s, revision %d)", src.ID[:8], fromEnv, src.CurrentRevision)
		d.event(r.Context(), id, "pending", message, a.ID)
		d.event(r.Context(), src.ID, src.Phase, fmt.Sprintf("revision %d promoted to %s as deployment %s", src.CurrentRevision, *req.environment(), id[:8]), a.ID)
		d.audit(r.Context(), a, "deployment.promote", "deployment", id, message, map[string]any{"from": src.ID, "revision": src.CurrentRevision, "environment": req.environment()})

		dep, err := d.st.GetDeployment(r.Context(), id)
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		outcome, err := d.submit(r.Context(), dep, actionDeploy, actionParams{FromDeploymentID: src.ID, FromRevision: src.CurrentRevision}, a, req.gateOptions, true)
		if err != nil {
			_ = d.st.UpdateDeploymentPhase(r.Context(), id, "failed")
			var ae *actionError
			if errors.As(err, &ae) {
				d.event(r.Context(), id, "failed", ae.msg, a.ID)
				if ae.extra == nil {
					ae.extra = map[string]any{}
				}
				ae.extra["deploymentId"] = id
			}
			writeActionError(w, d.log, err)
			return
		}
		writeJSON(w, http.StatusAccepted, outcome)
	}
}

// stackActionOutcomes maps a stack-level start/stop/restart action to the
// deployment phase and event message recorded once every container has
// been acted on. "remove" isn't here — handleDeploymentAction handles it
// separately, since removal also has to tear down the deployment's
// networks via a single UndeployCommand rather than fanning a
// per-container action out across whatever containers currently exist.
var stackActionOutcomes = map[string]struct{ phase, message string }{
	"start":   {phase: "running", message: "stack started"},
	"stop":    {phase: "stopped", message: "stack stopped"},
	"restart": {phase: "running", message: "stack restarted"},
}

// handleDeploymentAction applies a start/stop/restart/remove action to
// every container in a deployment — the stack-level counterpart to
// handleContainerAction/handleBulkContainerAction, which only ever knew
// about individually-named {server, container} pairs.
func handleDeploymentAction(d *deployer) http.HandlerFunc {
	type request struct {
		Action string `json:"action"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		deployment, ok := d.loadDeploymentOr404(w, r)
		if !ok {
			return
		}
		id := deployment.ID
		a := actorFromRequest(r)

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if req.Action == "remove" {
			handleUndeployAction(w, r, d, deployment)
			return
		}

		outcome, ok := stackActionOutcomes[req.Action]
		if !ok {
			http.Error(w, "action must be one of start, stop, restart, remove", http.StatusBadRequest)
			return
		}
		action, err := parseContainerAction(req.Action)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		containers, err := d.st.ListContainersFiltered(r.Context(), store.ContainerFilter{DeploymentID: id})
		if err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if len(containers) == 0 {
			http.Error(w, "deployment has no containers to act on", http.StatusNotFound)
			return
		}

		results := make([]bulkActionResult, len(containers))
		var wg sync.WaitGroup
		for i, c := range containers {
			wg.Add(1)
			go func(i int, c store.FleetContainer) {
				defer wg.Done()
				target := bulkActionTarget{ServerID: c.ServerID, ContainerID: c.ContainerID}
				results[i] = bulkDispatchOne(d.log, d.dispatcher, d.opWaiter, target, action, 0, false)
			}(i, c)
		}
		wg.Wait()

		allSucceeded := true
		for _, res := range results {
			if !res.Success {
				allSucceeded = false
				break
			}
		}
		message := outcome.message
		if !allSucceeded {
			message += " (some containers failed — see results)"
		}
		if err := d.st.UpdateDeploymentPhase(r.Context(), id, outcome.phase); err != nil {
			d.log.Error("failed to update deployment phase", "error", err)
		}
		d.event(r.Context(), id, outcome.phase, message, a.ID)
		d.audit(r.Context(), a, "deployment."+req.Action, "deployment", id, message, nil)

		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}
}

// handleUndeployAction is handleDeploymentAction's "remove" branch: unlike
// start/stop/restart, removal also has to tear down the deployment's
// networks (see docker.Undeploy), so it dispatches a single
// UndeployCommand rather than fanning a per-container action out.
func handleUndeployAction(w http.ResponseWriter, r *http.Request, d *deployer, deployment *store.Deployment) {
	requestID, ok := newRequestID(w, d.log)
	if !ok {
		return
	}

	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_Undeploy{
			Undeploy: &agentv1.UndeployCommand{
				RequestId:    requestID,
				ServerId:     deployment.ServerID,
				DeploymentId: deployment.ID,
			},
		},
	}
	result, err := sendAndAwaitContainerOp(d.dispatcher, d.opWaiter, deployment.ServerID, requestID, cmd)
	if err != nil {
		if errors.Is(err, deploy.ErrAgentNotConnected) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
		return
	}

	if result.GetSuccess() {
		if err := d.st.UpdateDeploymentPhase(r.Context(), deployment.ID, "removed"); err != nil {
			d.log.Error("failed to update deployment phase", "error", err)
		}
		a := actorFromRequest(r)
		d.event(r.Context(), deployment.ID, "removed", "stack removed", a.ID)
		d.audit(r.Context(), a, "deployment.remove", "deployment", deployment.ID, "stack removed", nil)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": result.GetSuccess(),
		"error":   result.GetErrorMessage(),
	})
}

// handleUpdateDeploymentMetadata sets a deployment's grouping/governance/
// rollout metadata after creation — owner (created_by) is fixed at
// creation time and not editable here. Every field is a full replace: the
// caller sends the whole desired state, not a partial patch.
func handleUpdateDeploymentMetadata(d *deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dep, ok := d.loadDeploymentOr404(w, r)
		if !ok {
			return
		}
		var req deploymentGovernanceFields
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := req.validate(); err != nil {
			writeActionError(w, d.log, err)
			return
		}
		if req.GitRef != "" && req.GitRef != dep.GitRef {
			if dep.ComposeFileID == nil {
				http.Error(w, "gitRef is only valid for a Git-linked Compose file", http.StatusBadRequest)
				return
			}
			if f, err := d.st.GetComposeFile(r.Context(), *dep.ComposeFileID); err != nil || f.GitRepositoryID == nil {
				http.Error(w, "gitRef is only valid for a Git-linked Compose file", http.StatusBadRequest)
				return
			}
		}

		m := store.DeploymentMetadata{
			Environment:    req.environment(),
			Tags:           req.Tags,
			ChangeRequest:  req.ChangeRequest,
			Notes:          req.Notes,
			RollbackPlan:   req.RollbackPlan,
			AutoRollback:   req.AutoRollback,
			UpdateStrategy: req.UpdateStrategy,
			AutoDeploy:     req.AutoDeploy,
			GitRef:         req.GitRef,
		}
		if err := d.st.UpdateDeploymentMetadata(r.Context(), dep.ID, m); err != nil {
			writeActionError(w, d.log, err)
			return
		}
		d.audit(r.Context(), actorFromRequest(r), "deployment.update_metadata", "deployment", dep.ID, "deployment metadata updated", map[string]any{
			"environment": m.Environment, "changeRequest": m.ChangeRequest, "rollbackPlan": m.RollbackPlan,
			"autoRollback": m.AutoRollback, "updateStrategy": m.UpdateStrategy, "autoDeploy": m.AutoDeploy, "gitRef": m.GitRef, "tags": m.Tags,
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// composeImages parses composeYAML far enough to list every service's
// image, for enforceImagePolicy to check before a stack is dispatched. A
// parse failure here is deliberately swallowed by the caller (deploy still
// proceeds and the agent's own parseCompose reports the real error) — this
// is a policy pre-check, not compose validation.
func composeImages(ctx context.Context, composeYAML string, env map[string]string) ([]string, error) {
	details := types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{{Filename: "compose.yaml", Content: []byte(composeYAML)}},
		Environment: env,
	}
	project, err := loader.LoadWithContext(ctx, details, func(o *loader.Options) {
		o.SetProjectName("policy-check", true)
		o.SkipConsistencyCheck = true
	})
	if err != nil {
		return nil, err
	}

	images := make([]string, 0, len(project.Services))
	for _, svc := range project.Services {
		images = append(images, svc.Image)
	}
	return images, nil
}
