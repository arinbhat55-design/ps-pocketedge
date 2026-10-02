package docker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRuntimeClientConfiguration(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///tmp/docker-env.sock")
	t.Setenv("DOCKER_API_VERSION", "1.51")
	for _, runtime := range []string{"docker", "podman"} {
		cli, err := NewClientForRuntime(runtime, "unix:///tmp/selected.sock")
		if err != nil {
			t.Fatal(err)
		}
		if cli.DaemonHost() != "unix:///tmp/selected.sock" {
			t.Fatal(cli.DaemonHost())
		}
		if runtime == "podman" && cli.ClientVersion() != "1.40" {
			t.Fatal(cli.ClientVersion())
		}
		cli.Close()
	}
	if _, err := NewClientForRuntime("invalid", ""); err == nil {
		t.Fatal("expected invalid runtime error")
	}
	cli, err := NewClientForRuntime("podman", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if cli.DaemonHost() != "unix:///tmp/docker-env.sock" {
		t.Fatal(cli.DaemonHost())
	}
}

func TestPodmanCompatibilityContainerList(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_API_VERSION", "1.51")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.40/containers/json" || r.URL.Query().Get("all") != "1" {
			t.Errorf("unexpected request: %s", r.URL.String())
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"Id":"podman-container","Names":["/web"],"Image":"nginx","State":"running","Status":"Up"}]`))
	}))
	defer server.Close()
	cli, err := NewClientForRuntime("podman", "tcp"+server.URL[4:])
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	containers, err := ListContainers(context.Background(), cli)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].ID != "podman-container" {
		t.Fatalf("containers: %+v", containers)
	}
}

func TestExplicitHostOverridesInvalidEnvironment(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://[")
	t.Setenv("DOCKER_CERT_PATH", "")
	for _, runtime := range []string{"docker", "podman"} {
		cli, err := NewClientForRuntime(runtime, "unix:///tmp/selected.sock")
		if err != nil {
			t.Fatalf("explicit %s host rejected because of inherited environment: %v", runtime, err)
		}
		cli.Close()
	}
}

func TestRuntimeDefaultSockets(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/pspe-user")
	cli, err := NewClientForRuntime("podman", "")
	if err != nil {
		t.Fatal(err)
	}
	expected := "unix:///tmp/pspe-user/podman/podman.sock"
	if os.Getuid() == 0 {
		expected = "unix:///run/podman/podman.sock"
	}
	if cli.DaemonHost() != expected {
		t.Fatalf("got %s, want %s", cli.DaemonHost(), expected)
	}
	cli.Close()
	t.Setenv("XDG_RUNTIME_DIR", "")
	cli, err = NewClientForRuntime("podman", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if os.Getuid() != 0 {
		expected = fmt.Sprintf("unix:///run/user/%d/podman/podman.sock", os.Getuid())
	}
	if cli.DaemonHost() != expected {
		t.Fatalf("got %s, want %s", cli.DaemonHost(), expected)
	}
}
