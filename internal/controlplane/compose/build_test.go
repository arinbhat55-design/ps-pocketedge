package compose

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const buildCompose = `
services:
  api:
    build:
      context: ./services/api
      dockerfile: docker/Dockerfile.prod
      target: runtime
      args:
        VERSION: ${APP_VERSION:-dev}
        REGION:
      labels: [team=core]
    ports: ["8080:80"]
  web:
    build: .
    pull_policy: build
  db:
    image: postgres:16
`

func TestBuildSpecs(t *testing.T) {
	specs, err := BuildSpecs(buildCompose, "deploy", map[string]string{"APP_VERSION": "1.2", "REGION": "eu"})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs", len(specs))
	}
	api, web := specs[0], specs[1]
	if api.Service != "api" || api.Context != "deploy/services/api" || api.Dockerfile != "docker/Dockerfile.prod" || api.Target != "runtime" {
		t.Fatalf("api spec = %+v", api)
	}
	if api.Args["VERSION"] != "1.2" || api.Args["REGION"] != "eu" || api.Labels["team"] != "core" {
		t.Fatalf("api args/labels = %v %v", api.Args, api.Labels)
	}
	if web.Context != "deploy" || web.Dockerfile != "Dockerfile" {
		t.Fatalf("web spec = %+v", web)
	}

	// Compose file at the repository root.
	specs, err = BuildSpecs(buildCompose, ".", nil)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Context != "services/api" || specs[0].Args["VERSION"] != "dev" {
		t.Fatalf("root spec = %+v", specs[0])
	}
	if _, ok := specs[0].Args["REGION"]; ok {
		t.Fatal("an arg with no value and no env var must stay unset")
	}
}

func TestBuildSpecsRejectsUnsafeContexts(t *testing.T) {
	for _, tc := range []struct{ build, want string }{
		{"https://github.com/x/y.git", "remote build contexts"},
		{"/srv/app", "must be relative"},
		{"../../outside", "outside the repository"},
		{"{context: ., dockerfile: ../Dockerfile}", "inside the build context"},
		{"{dockerfile_inline: 'FROM scratch'}", "dockerfile_inline"},
		{"{context: ., ssh: default}", "build ssh isn't supported"},
		{"{context: ., secrets: [token]}", "build secrets isn't supported"},
	} {
		content := "services:\n  app:\n    build: " + tc.build + "\n"
		_, err := BuildSpecs(content, "deploy", nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("build %s: got %v, want error containing %q", tc.build, err, tc.want)
		}
	}
}

func TestBuildTagIsDeterministic(t *testing.T) {
	spec := BuildSpec{Service: "My_API", Context: "api", Dockerfile: "Dockerfile", Args: map[string]string{"A": "1", "B": "2"}}
	commit := "0123456789abcdef0123456789abcdef01234567"
	tag := BuildTag("3f2a9c1e-aaaa-bbbb-cccc-000000000000", commit, spec)
	if tag != BuildTag("3f2a9c1e-aaaa-bbbb-cccc-000000000000", commit, spec) {
		t.Fatal("same inputs must give the same tag")
	}
	if !strings.HasPrefix(tag, "pspe-build/3f2a9c1e-my-api:0123456789ab-") || !IsBuiltImage(tag) {
		t.Fatalf("tag = %s", tag)
	}
	changed := spec
	changed.Args = map[string]string{"A": "1", "B": "3"}
	if BuildTag("3f2a9c1e", commit, changed) == BuildTag("3f2a9c1e", commit, spec) {
		t.Fatal("a changed build arg must change the tag")
	}
	if BuildTag("3f2a9c1e", "ffffffffffffffffffffffffffffffffffffffff", spec) == BuildTag("3f2a9c1e", commit, spec) {
		t.Fatal("a new commit must change the tag")
	}
}

func TestPinBuiltImages(t *testing.T) {
	pinned, err := PinBuiltImages(buildCompose, map[string]string{"api": "pspe-build/x-api:1", "web": "pspe-build/x-web:1"})
	if err != nil {
		t.Fatal(err)
	}
	if HasBuild(pinned) || strings.Contains(pinned, "pull_policy") {
		t.Fatalf("build sections left in:\n%s", pinned)
	}
	var doc struct {
		Services map[string]struct {
			Image string   `yaml:"image"`
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(pinned), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Services["api"].Image != "pspe-build/x-api:1" || doc.Services["web"].Image != "pspe-build/x-web:1" || doc.Services["db"].Image != "postgres:16" {
		t.Fatalf("images = %+v", doc.Services)
	}
	if len(doc.Services["api"].Ports) != 1 {
		t.Fatalf("other settings lost:\n%s", pinned)
	}
	if !HasBuild(buildCompose) {
		t.Fatal("HasBuild missed the build sections")
	}
}

func TestDescribeChanges(t *testing.T) {
	oldContent := `
services:
  api:
    build: .
    ports: ["8080:80"]
    environment:
      A: "1"
      B: "2"
  old:
    image: busybox
`
	newContent := `
services:
  api:
    build: .   # comments and style don't count
    ports: ["9090:80"]
    environment: ["A=1", "B=3", "C=4"]
  worker:
    image: busybox
volumes:
  data: {}
`
	got := strings.Join(DescribeChanges(oldContent, newContent), "; ")
	want := `service "api": environment (~B, +C), ports; service "old" removed; service "worker" added; top-level volumes`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if d := DescribeChanges(oldContent, oldContent); len(d) != 0 {
		t.Fatalf("no changes expected, got %v", d)
	}
}
