package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

// Podman's compatibility API is pinned below 1.44, where the SDK refuses a
// health-check start interval client-side. The deploy must still succeed.
func TestCreateContainerHealthStartIntervalOnPodman(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	var created container.Config
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1.40/containers/create" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Errorf("decode create body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"Id":"podman-container"}`))
	}))
	defer server.Close()
	cli, err := NewClientForRuntime("podman", "tcp"+server.URL[4:])
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	interval := types.Duration(10 * time.Second)
	startInterval := types.Duration(time.Second)
	svc := types.ServiceConfig{
		Image: "postgres:16",
		HealthCheck: &types.HealthCheckConfig{
			Test:          types.HealthCheckTest{"CMD", "pg_isready"},
			Interval:      &interval,
			StartInterval: &startInterval,
		},
	}
	id, err := createContainer(context.Background(), cli, "dep1", "db", "postgres", 1, svc, "net1", &network.EndpointSettings{}, nil)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if id != "podman-container" {
		t.Fatalf("id: %s", id)
	}
	if created.Healthcheck == nil || created.Healthcheck.Interval != 10*time.Second {
		t.Fatalf("health check not sent: %+v", created.Healthcheck)
	}
}
