package docker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestServiceOrderRespectsDependsOn(t *testing.T) {
	project := &types.Project{Services: types.Services{
		"web":    {Name: "web", DependsOn: types.DependsOnConfig{"api": {}}},
		"api":    {Name: "api", DependsOn: types.DependsOnConfig{"db": {}, "cache": {}}},
		"db":     {Name: "db"},
		"cache":  {Name: "cache"},
		"worker": {Name: "worker", DependsOn: types.DependsOnConfig{"db": {}}},
	}}
	order, err := ServiceOrder(project)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cache", "db", "api", "web", "worker"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestServiceOrderDetectsCycle(t *testing.T) {
	project := &types.Project{Services: types.Services{
		"a": {Name: "a", DependsOn: types.DependsOnConfig{"b": {}}},
		"b": {Name: "b", DependsOn: types.DependsOnConfig{"a": {}}},
	}}
	_, err := ServiceOrder(project)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v, want a dependency cycle error", err)
	}
}

func TestServiceOrderRejectsUndefinedDependency(t *testing.T) {
	project := &types.Project{Services: types.Services{
		"a": {Name: "a", DependsOn: types.DependsOnConfig{"missing": {}}},
	}}
	if _, err := ServiceOrder(project); err == nil {
		t.Fatal("expected an error for an undefined dependency")
	}
}

func TestDesiredReplicas(t *testing.T) {
	three, two := 3, 2
	cases := []struct {
		name     string
		svc      types.ServiceConfig
		override int32
		want     int
	}{
		{"default", types.ServiceConfig{}, 0, 1},
		{"scale", types.ServiceConfig{Scale: &two}, 0, 2},
		{"deploy.replicas wins over scale", types.ServiceConfig{Scale: &two, Deploy: &types.DeployConfig{Replicas: &three}}, 0, 3},
		{"override wins", types.ServiceConfig{Deploy: &types.DeployConfig{Replicas: &three}}, 5, 5},
	}
	for _, c := range cases {
		if got := DesiredReplicas(c.svc, c.override); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestCheckScalable(t *testing.T) {
	fixed := types.ServiceConfig{Ports: []types.ServicePortConfig{{Target: 80, Published: "8080"}}}
	unpublished := types.ServiceConfig{Ports: []types.ServicePortConfig{{Target: 80}}}
	if err := checkScalable("web", fixed, 1); err != nil {
		t.Errorf("one replica with a fixed port should be fine: %v", err)
	}
	if err := checkScalable("web", fixed, 2); err == nil {
		t.Error("two replicas with a fixed host port should be rejected")
	}
	if err := checkScalable("web", unpublished, 4); err != nil {
		t.Errorf("unpublished ports should scale: %v", err)
	}
}

func TestContainerNameKeepsReplicaOneName(t *testing.T) {
	if got := containerName("d1", "web", 1); got != "pe-d1-web" {
		t.Errorf("replica 1 = %q", got)
	}
	if got := containerName("d1", "web", 3); got != "pe-d1-web-3" {
		t.Errorf("replica 3 = %q", got)
	}
}

func TestServiceNetworksDefaultsAndPriority(t *testing.T) {
	if got := serviceNetworks(types.ServiceConfig{}); !reflect.DeepEqual(got, []string{"default"}) {
		t.Errorf("no networks = %v", got)
	}
	svc := types.ServiceConfig{Networks: map[string]*types.ServiceNetworkConfig{
		"backend":  nil,
		"frontend": {Priority: 10},
	}}
	if got := serviceNetworks(svc); !reflect.DeepEqual(got, []string{"frontend", "backend"}) {
		t.Errorf("priority order = %v", got)
	}
}

func TestLiveContainersSkipsSetAside(t *testing.T) {
	list := []serviceContainer{{Name: "pe-d-cache"}, {Name: "pe-d-cache-prev", SetAside: true}}
	live := liveContainers(list)
	if len(live) != 1 || live[0].Name != "pe-d-cache" {
		t.Fatalf("live = %+v", live)
	}
}
