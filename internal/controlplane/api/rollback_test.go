package api

import (
	"testing"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func TestContainerConfigFromStateSwapsImageKeepsRest(t *testing.T) {
	state := &store.ContainerState{
		Name: "my-app",
		Ports: []store.ContainerPort{
			{PrivatePort: 8080, PublicPort: 80, Type: "tcp"},
			// A container-only exposed port with no host binding (PublicPort
			// 0) must be dropped, not turned into a HostPort:0 mapping.
			{PrivatePort: 9000, PublicPort: 0, Type: "tcp"},
		},
		Mounts: []store.ContainerMount{
			{Type: "volume", Name: "app-data", Destination: "/data", ReadWrite: true},
			// A bind mount must be skipped — only named volumes are
			// supported for standalone containers (see ensureVolumes'
			// doc comment in the agent's deploy.go).
			{Type: "bind", Name: "", Source: "/host/path", Destination: "/bind", ReadWrite: true},
			// A read-only volume must map to ReadOnly: true.
			{Type: "volume", Name: "app-config", Destination: "/config", ReadWrite: false},
		},
	}
	detail := &agentv1.ContainerDetail{
		Env:                        []string{"FOO=bar"},
		RestartPolicyName:          "unless-stopped",
		RestartPolicyMaxRetryCount: 3,
	}

	config := containerConfigFromState(state, detail, "myapp:v2")

	if config.Image != "myapp:v2" {
		t.Errorf("Image = %q, want %q", config.Image, "myapp:v2")
	}
	if config.Name != "my-app" {
		t.Errorf("Name = %q, want %q", config.Name, "my-app")
	}
	if len(config.Env) != 1 || config.Env[0] != "FOO=bar" {
		t.Errorf("Env = %v, want [FOO=bar]", config.Env)
	}
	if config.RestartPolicyName != "unless-stopped" || config.RestartPolicyMaxRetryCount != 3 {
		t.Errorf("restart policy = %q/%d, want unless-stopped/3", config.RestartPolicyName, config.RestartPolicyMaxRetryCount)
	}

	if len(config.Ports) != 1 {
		t.Fatalf("Ports = %v, want exactly 1 (the unpublished port must be dropped)", config.Ports)
	}
	if config.Ports[0].ContainerPort != 8080 || config.Ports[0].HostPort != 80 {
		t.Errorf("Ports[0] = %+v, want ContainerPort:8080 HostPort:80", config.Ports[0])
	}

	if len(config.Volumes) != 2 {
		t.Fatalf("Volumes = %v, want exactly 2 (the bind mount must be dropped)", config.Volumes)
	}
	byTarget := map[string]*agentv1.ContainerVolumeSpec{}
	for _, v := range config.Volumes {
		byTarget[v.Target] = v
	}
	if v := byTarget["/data"]; v == nil || v.VolumeName != "app-data" || v.ReadOnly {
		t.Errorf("volume for /data = %+v, want VolumeName:app-data ReadOnly:false", v)
	}
	if v := byTarget["/config"]; v == nil || v.VolumeName != "app-config" || !v.ReadOnly {
		t.Errorf("volume for /config = %+v, want VolumeName:app-config ReadOnly:true", v)
	}
}

func TestContainerConfigFromStateNoPortsOrVolumes(t *testing.T) {
	state := &store.ContainerState{Name: "bare"}
	detail := &agentv1.ContainerDetail{}

	config := containerConfigFromState(state, detail, "bare:latest")

	if len(config.Ports) != 0 {
		t.Errorf("Ports = %v, want empty", config.Ports)
	}
	if len(config.Volumes) != 0 {
		t.Errorf("Volumes = %v, want empty", config.Volumes)
	}
}
