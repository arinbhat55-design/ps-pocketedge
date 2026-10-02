package compose

import (
	"strings"
	"testing"
)

func TestParseValidSimpleFile(t *testing.T) {
	content := `
services:
  web:
    image: nginx:latest
    ports:
      - "8080:80"
    environment:
      FOO: bar
    volumes:
      - data:/var/www
    restart: unless-stopped
`
	result := Parse(content)
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %v", result.Errors)
	}
	if !result.VisualEditable {
		t.Fatalf("expected visual-editable")
	}
	if len(result.ServiceNames) != 1 || result.ServiceNames[0] != "web" {
		t.Fatalf("unexpected service names: %v", result.ServiceNames)
	}
	svc := result.Services[0]
	if svc.Image != "nginx:latest" || svc.Restart != "unless-stopped" {
		t.Fatalf("unexpected service draft: %+v", svc)
	}
	if svc.Environment["FOO"] != "bar" {
		t.Fatalf("unexpected environment: %+v", svc.Environment)
	}
}

func TestParseEnvironmentListForm(t *testing.T) {
	content := `
services:
  web:
    image: nginx
    environment:
      - FOO=bar
      - BAZ=qux
`
	result := Parse(content)
	if !result.Valid || !result.VisualEditable {
		t.Fatalf("expected valid+editable, got %+v", result)
	}
	env := result.Services[0].Environment
	if env["FOO"] != "bar" || env["BAZ"] != "qux" {
		t.Fatalf("unexpected environment: %+v", env)
	}
}

func TestParseInvalidYAMLSyntax(t *testing.T) {
	result := Parse("services: [this is not: valid")
	if result.Valid {
		t.Fatalf("expected invalid")
	}
	if len(result.Errors) == 0 {
		t.Fatalf("expected an error message")
	}
}

func TestParseMissingServices(t *testing.T) {
	result := Parse("version: '3'\n")
	if result.Valid {
		t.Fatalf("expected invalid due to missing services")
	}
}

func TestParseEmptyContent(t *testing.T) {
	result := Parse("   \n")
	if result.Valid {
		t.Fatalf("expected invalid for empty content")
	}
}

func TestParseUnsupportedKeyMarksNotVisualEditable(t *testing.T) {
	content := `
services:
  web:
    image: nginx
    build: .
`
	result := Parse(content)
	if !result.Valid {
		t.Fatalf("build: is valid compose, should not fail structural validation: %v", result.Errors)
	}
	if result.VisualEditable {
		t.Fatalf("expected not visual-editable due to unsupported 'build' key")
	}
}

func TestRenderRoundTrip(t *testing.T) {
	services := []ServiceDraft{
		{
			Name:        "web",
			Image:       "nginx:latest",
			Ports:       []string{"8080:80"},
			Environment: map[string]string{"FOO": "bar"},
			Volumes:     []string{"data:/var/www", "./host-dir:/mnt"},
			Restart:     "unless-stopped",
		},
	}
	rendered, err := Render(services)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(rendered, `image: "nginx:latest"`) {
		t.Fatalf("rendered output missing image: %s", rendered)
	}
	if !strings.Contains(rendered, "volumes:\n  data: {}\n") {
		t.Fatalf("expected named volume 'data' to be declared, got: %s", rendered)
	}
	if strings.Contains(rendered, "host-dir: {}") {
		t.Fatalf("bind-mounted host path should not be declared as a named volume: %s", rendered)
	}

	reparsed := Parse(rendered)
	if !reparsed.Valid || !reparsed.VisualEditable {
		t.Fatalf("rendered output did not round-trip: %+v errors=%v", reparsed, reparsed.Errors)
	}
	if reparsed.Services[0].Image != "nginx:latest" {
		t.Fatalf("round-tripped image mismatch: %+v", reparsed.Services[0])
	}
}

func TestRenderResourceLimitsRoundTrip(t *testing.T) {
	services := []ServiceDraft{
		{
			Name:                   "web",
			Image:                  "nginx:latest",
			NanoCPUs:               1_500_000_000, // 1.5 cores
			MemoryLimitBytes:       536_870_912,   // 512M
			MemoryReservationBytes: 268_435_456,   // 256M
		},
	}
	rendered, err := Render(services)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(rendered, `cpus: "1.5"`) {
		t.Fatalf("rendered output missing cpus limit: %s", rendered)
	}

	reparsed := Parse(rendered)
	if !reparsed.Valid || !reparsed.VisualEditable {
		t.Fatalf("rendered output did not round-trip: %+v errors=%v", reparsed, reparsed.Errors)
	}
	svc := reparsed.Services[0]
	if svc.NanoCPUs != 1_500_000_000 {
		t.Fatalf("expected NanoCPUs=1.5e9, got %d", svc.NanoCPUs)
	}
	if svc.MemoryLimitBytes != 536_870_912 {
		t.Fatalf("expected MemoryLimitBytes=512M, got %d", svc.MemoryLimitBytes)
	}
	if svc.MemoryReservationBytes != 268_435_456 {
		t.Fatalf("expected MemoryReservationBytes=256M, got %d", svc.MemoryReservationBytes)
	}
}

func TestParseDeployWithReplicasMarksNotVisualEditable(t *testing.T) {
	content := `
services:
  web:
    image: nginx
    deploy:
      replicas: 3
      resources:
        limits:
          cpus: "1"
`
	result := Parse(content)
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %v", result.Errors)
	}
	if result.VisualEditable {
		t.Fatalf("expected not visual-editable due to 'replicas'")
	}
}

func TestRenderHealthCheckRoundTrip(t *testing.T) {
	services := []ServiceDraft{
		{
			Name:                          "web",
			Image:                         "nginx:latest",
			HealthCheckTest:               "curl -f http://localhost || exit 1",
			HealthCheckIntervalSeconds:    30,
			HealthCheckTimeoutSeconds:     5,
			HealthCheckRetries:            3,
			HealthCheckStartPeriodSeconds: 10,
		},
	}
	rendered, err := Render(services)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(rendered, "healthcheck:") {
		t.Fatalf("rendered output missing healthcheck: %s", rendered)
	}

	reparsed := Parse(rendered)
	if !reparsed.Valid || !reparsed.VisualEditable {
		t.Fatalf("rendered output did not round-trip: %+v errors=%v", reparsed, reparsed.Errors)
	}
	svc := reparsed.Services[0]
	if svc.HealthCheckTest != services[0].HealthCheckTest {
		t.Fatalf("expected test %q, got %q", services[0].HealthCheckTest, svc.HealthCheckTest)
	}
	if svc.HealthCheckIntervalSeconds != 30 || svc.HealthCheckTimeoutSeconds != 5 ||
		svc.HealthCheckRetries != 3 || svc.HealthCheckStartPeriodSeconds != 10 {
		t.Fatalf("healthcheck timing fields did not round-trip: %+v", svc)
	}
}

func TestParseHealthCheckPlainStringForm(t *testing.T) {
	content := `
services:
  web:
    image: nginx
    healthcheck:
      test: "curl -f http://localhost"
      interval: 15s
`
	result := Parse(content)
	if !result.Valid || !result.VisualEditable {
		t.Fatalf("expected valid+editable, got %+v errors=%v", result, result.Errors)
	}
	if result.Services[0].HealthCheckTest != "curl -f http://localhost" {
		t.Fatalf("unexpected test command: %q", result.Services[0].HealthCheckTest)
	}
	if result.Services[0].HealthCheckIntervalSeconds != 15 {
		t.Fatalf("expected interval=15s, got %d", result.Services[0].HealthCheckIntervalSeconds)
	}
}

func TestParseHealthCheckExecFormMarksNotVisualEditable(t *testing.T) {
	content := `
services:
  web:
    image: nginx
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost"]
`
	result := Parse(content)
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %v", result.Errors)
	}
	if result.VisualEditable {
		t.Fatalf("expected not visual-editable for exec-form healthcheck test")
	}
}

func TestRenderRejectsMissingImage(t *testing.T) {
	_, err := Render([]ServiceDraft{{Name: "web"}})
	if err == nil {
		t.Fatalf("expected error for missing image")
	}
}

func TestRenderRejectsInvalidServiceName(t *testing.T) {
	_, err := Render([]ServiceDraft{{Name: "web service!", Image: "nginx"}})
	if err == nil {
		t.Fatalf("expected error for invalid service name")
	}
}

func TestRenderRejectsEmptyServiceList(t *testing.T) {
	_, err := Render(nil)
	if err == nil {
		t.Fatalf("expected error for empty service list")
	}
}

func TestRenderRejectsDuplicateServiceNames(t *testing.T) {
	_, err := Render([]ServiceDraft{
		{Name: "web", Image: "nginx"},
		{Name: "web", Image: "nginx:alpine"},
	})
	if err == nil {
		t.Fatalf("expected error for duplicate service names")
	}
}
