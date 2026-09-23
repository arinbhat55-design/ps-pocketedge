package api

import (
	"context"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// loadTestProject parses content the same way handlePreviewDeployment
// does, for tests that need a real *types.Project rather than
// hand-constructing compose-go's structs.
func loadTestProject(t *testing.T, content string) *types.Project {
	t.Helper()
	details := types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{{Filename: "compose.yaml", Content: []byte(content)}},
		Environment: map[string]string{},
	}
	project, err := loader.LoadWithContext(context.Background(), details, func(o *loader.Options) {
		o.SetProjectName("test", true)
		o.SkipConsistencyCheck = true
	})
	if err != nil {
		t.Fatalf("failed to load test compose file: %v", err)
	}
	return project
}

func TestComputeResourceCheckSufficient(t *testing.T) {
	resources := store.ResourceSnapshot{
		CPUPercent:       25,
		MemPercent:       25,
		DiskPercent:      40,
		TotalMemoryBytes: 8 << 30, // 8Gi
		NumCPUs:          4,
	}
	services := []deploymentPreviewService{
		{Name: "web", NanoCPUs: 1_000_000_000, MemoryLimitBytes: 512 << 20},
	}

	check := computeResourceCheck(resources, services)

	if !check.SufficientMemory || !check.SufficientCPU {
		t.Fatalf("expected sufficient resources, got %+v", check)
	}
	if check.RiskScore != "low" {
		t.Fatalf("expected low risk, got %q", check.RiskScore)
	}
}

func TestComputeResourceCheckInsufficientMemory(t *testing.T) {
	resources := store.ResourceSnapshot{
		MemPercent:       90,
		TotalMemoryBytes: 8 << 30, // 8Gi total, ~819Mi available
		NumCPUs:          4,
	}
	services := []deploymentPreviewService{
		{Name: "db", MemoryLimitBytes: 4 << 30}, // requests 4Gi
	}

	check := computeResourceCheck(resources, services)

	if check.SufficientMemory {
		t.Fatalf("expected insufficient memory, got %+v", check)
	}
	if check.RiskScore != "high" {
		t.Fatalf("expected high risk, got %q", check.RiskScore)
	}
}

func TestComputeResourceCheckHighDiskUsageIsMediumRisk(t *testing.T) {
	resources := store.ResourceSnapshot{
		MemPercent:       10,
		DiskPercent:      92,
		TotalMemoryBytes: 8 << 30,
		NumCPUs:          4,
	}
	services := []deploymentPreviewService{{Name: "web"}}

	check := computeResourceCheck(resources, services)

	if check.RiskScore != "medium" {
		t.Fatalf("expected medium risk from high disk usage, got %q", check.RiskScore)
	}
}

func TestComputeResourceCheckNoDeclaredLimitsIsTriviallySufficient(t *testing.T) {
	resources := store.ResourceSnapshot{
		MemPercent:       95,
		TotalMemoryBytes: 8 << 30,
		NumCPUs:          4,
	}
	services := []deploymentPreviewService{{Name: "web"}}

	check := computeResourceCheck(resources, services)

	if !check.SufficientMemory || !check.SufficientCPU {
		t.Fatalf("expected trivially sufficient when nothing is declared, got %+v", check)
	}
	if check.RiskScore != "low" {
		t.Fatalf("expected low risk, got %q", check.RiskScore)
	}
}

func TestMatchPortConflictsDetectsConflict(t *testing.T) {
	requests := []requestedPort{{service: "web", hostPort: 8080, protocol: "tcp"}}
	containers := []store.FleetContainer{
		{
			ContainerID: "c1",
			Ports:       []store.ContainerPort{{PublicPort: 8080, Type: "tcp"}},
		},
	}

	conflicts := matchPortConflicts(requests, containers)

	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %+v", conflicts)
	}
	if conflicts[0].ContainerID != "c1" || conflicts[0].Service != "web" {
		t.Fatalf("unexpected conflict details: %+v", conflicts[0])
	}
}

func TestMatchPortConflictsIgnoresDifferentProtocol(t *testing.T) {
	requests := []requestedPort{{service: "web", hostPort: 8080, protocol: "tcp"}}
	containers := []store.FleetContainer{
		{
			ContainerID: "c1",
			Ports:       []store.ContainerPort{{PublicPort: 8080, Type: "udp"}},
		},
	}

	conflicts := matchPortConflicts(requests, containers)

	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts across different protocols, got %+v", conflicts)
	}
}

func TestMatchPortConflictsNoneWhenPortFree(t *testing.T) {
	requests := []requestedPort{{service: "web", hostPort: 9090, protocol: "tcp"}}
	containers := []store.FleetContainer{
		{
			ContainerID: "c1",
			Ports:       []store.ContainerPort{{PublicPort: 8080, Type: "tcp"}},
		},
	}

	conflicts := matchPortConflicts(requests, containers)

	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %+v", conflicts)
	}
}

func TestValidateVolumePathsFlagsNonAbsolutePath(t *testing.T) {
	services := []deploymentPreviewService{
		{Name: "web", Volumes: []string{"data:relative/path"}},
	}
	warnings := validateVolumePaths(services)
	if len(warnings) != 1 || warnings[0].Message != "mount path must be absolute" {
		t.Fatalf("expected one absolute-path warning, got %+v", warnings)
	}
}

func TestValidateVolumePathsFlagsDuplicateTarget(t *testing.T) {
	services := []deploymentPreviewService{
		{Name: "web", Volumes: []string{"data:/var/lib/data", "cache:/var/lib/data"}},
	}
	warnings := validateVolumePaths(services)
	if len(warnings) != 1 || warnings[0].Message != "mount path is used by more than one volume on this service" {
		t.Fatalf("expected one duplicate-target warning, got %+v", warnings)
	}
}

func TestValidateVolumePathsNoWarningsForValidVolumes(t *testing.T) {
	services := []deploymentPreviewService{
		{Name: "web", Volumes: []string{"data:/var/lib/data", "cache:/var/lib/cache:ro"}},
	}
	if warnings := validateVolumePaths(services); len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %+v", warnings)
	}
}

func TestDetectMissingSecretsFindsUnsetVariable(t *testing.T) {
	content := "services:\n  web:\n    image: nginx\n    environment:\n      - DB_PASSWORD=${DB_PASSWORD}\n"
	missing := detectMissingSecrets(content, map[string]string{})
	if len(missing) != 1 || missing[0] != "DB_PASSWORD" {
		t.Fatalf("expected [DB_PASSWORD], got %v", missing)
	}
}

func TestDetectMissingSecretsIgnoresVariableWithFallback(t *testing.T) {
	content := "services:\n  web:\n    image: nginx\n    environment:\n      - LOG_LEVEL=${LOG_LEVEL:-info}\n"
	missing := detectMissingSecrets(content, map[string]string{})
	if len(missing) != 0 {
		t.Fatalf("expected no missing vars (has a fallback), got %v", missing)
	}
}

func TestDetectMissingSecretsIgnoresProvidedVariable(t *testing.T) {
	content := "services:\n  web:\n    image: nginx\n    environment:\n      - DB_PASSWORD=${DB_PASSWORD}\n"
	missing := detectMissingSecrets(content, map[string]string{"DB_PASSWORD": "s3cret"})
	if len(missing) != 0 {
		t.Fatalf("expected no missing vars (provided), got %v", missing)
	}
}

func TestValidateNetworkConfigurationNoWarningForImplicitDefault(t *testing.T) {
	project := loadTestProject(t, "services:\n  web:\n    image: nginx\n")
	if warnings := validateNetworkConfiguration(project); len(warnings) != 0 {
		t.Fatalf("expected no warnings for the implicit default network, got %v", warnings)
	}
}

func TestValidateNetworkConfigurationAllowsCustomBridgeNetwork(t *testing.T) {
	content := `
services:
  web:
    image: nginx
    networks:
      - frontend
networks:
  frontend: {}
`
	project := loadTestProject(t, content)
	if warnings := validateNetworkConfiguration(project); len(warnings) != 0 {
		t.Fatalf("custom bridge networks are supported, got warnings %v", warnings)
	}
}

func TestValidateNetworkConfigurationFlagsUnsupportedNetworks(t *testing.T) {
	content := `
services:
  web:
    image: nginx
    network_mode: host
  api:
    image: nginx
    networks: [mesh, shared]
networks:
  mesh:
    driver: overlay
  shared:
    external: true
`
	project := loadTestProject(t, content)
	warnings := strings.Join(validateNetworkConfiguration(project), "\n")
	for _, want := range []string{`driver "overlay"`, `"shared" is external`, `network_mode "host"`} {
		if !strings.Contains(warnings, want) {
			t.Errorf("missing warning %q in:\n%s", want, warnings)
		}
	}
}
