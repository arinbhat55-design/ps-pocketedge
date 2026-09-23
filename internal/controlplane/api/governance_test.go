package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

func TestCompareRuntime(t *testing.T) {
	expected := []expectedContainer{
		{name: "pe-d-web", image: "nginx"},
		{name: "pe-d-web-2", image: "nginx"},
		{name: "pe-d-db", image: "postgres:16"},
		{name: "pe-d-migrate", image: "app", oneShot: true},
	}
	actual := []store.FleetContainer{
		{Name: "pe-d-web", State: "running", Image: "nginx:latest"},
		{Name: "pe-d-db", State: "exited", Image: "postgres:15"},
		{Name: "pe-d-migrate", State: "exited", Image: "app"},
		{Name: "pe-d-old", State: "running", Image: "x"},
	}
	rd := compareRuntime(expected, actual)
	if !rd.Drifted {
		t.Fatal("expected drift")
	}
	if len(rd.Missing) != 1 || rd.Missing[0] != "pe-d-web-2" {
		t.Errorf("missing = %v", rd.Missing)
	}
	if len(rd.Unexpected) != 1 || rd.Unexpected[0] != "pe-d-old" {
		t.Errorf("unexpected = %v", rd.Unexpected)
	}
	if len(rd.NotRunning) != 1 {
		t.Errorf("notRunning = %v (a one-shot that exited shouldn't count)", rd.NotRunning)
	}
	if len(rd.ImageMismatch) != 1 {
		t.Errorf("imageMismatch = %v (nginx vs nginx:latest must match)", rd.ImageMismatch)
	}
}

func TestCompareRuntimeNoDrift(t *testing.T) {
	rd := compareRuntime([]expectedContainer{{name: "pe-d-web", image: "docker.io/library/nginx"}},
		[]store.FleetContainer{{Name: "/pe-d-web", State: "running", Image: "nginx"}})
	if rd.Drifted {
		t.Fatalf("unexpected drift: %+v", rd)
	}
}

func TestCheckPolicyRequirements(t *testing.T) {
	p := &store.EnvironmentPolicy{Environment: "production", RequireChangeRequest: true, RequireRollbackPlan: true}
	var ae *actionError
	if err := checkPolicyRequirements(p, actionDeploy, "", "plan"); !errors.As(err, &ae) || ae.status != http.StatusBadRequest {
		t.Errorf("missing change request should be a 400, got %v", err)
	}
	if err := checkPolicyRequirements(p, actionDeploy, "CR-1", ""); err == nil {
		t.Error("missing rollback plan should fail")
	}
	if err := checkPolicyRequirements(p, actionRollback, "CR-1", ""); err != nil {
		t.Errorf("a rollback doesn't need a rollback plan: %v", err)
	}
	if err := checkPolicyRequirements(nil, actionDeploy, "", ""); err != nil {
		t.Errorf("no policy should pass: %v", err)
	}
}

func TestValidateScale(t *testing.T) {
	project := loadTestProject(t, `
services:
  web:
    image: nginx
    ports: ["8080:80"]
  worker:
    image: busybox
`)
	if err := validateScale(project, "worker", 3); err != nil {
		t.Errorf("worker should scale: %v", err)
	}
	if err := validateScale(project, "web", 2); err == nil {
		t.Error("web publishes a fixed host port and shouldn't scale past 1")
	}
	if err := validateScale(project, "web", 1); err != nil {
		t.Errorf("one replica is fine: %v", err)
	}
	if err := validateScale(project, "missing", 1); err == nil {
		t.Error("unknown service should fail")
	}
	if err := validateScale(project, "worker", maxReplicas+1); err == nil {
		t.Error("too many replicas should fail")
	}
}

func TestNormalizeImage(t *testing.T) {
	cases := map[string]string{
		"nginx":                        "nginx:latest",
		"docker.io/library/nginx:1.27": "nginx:1.27",
		"ghcr.io/org/app":              "ghcr.io/org/app:latest",
		"localhost:5000/app":           "localhost:5000/app:latest",
		"app@sha256:abc":               "app@sha256:abc",
	}
	for in, want := range cases {
		if got := normalizeImage(in); got != want {
			t.Errorf("normalizeImage(%q) = %q, want %q", in, got, want)
		}
	}
}
