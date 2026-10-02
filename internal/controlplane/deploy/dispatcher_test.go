package deploy

import (
	"errors"
	"strings"
	"testing"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func TestSendResolvesEnvWithoutMutatingCaller(t *testing.T) {
	d := NewDispatcher()
	ch, unregister := d.Register("srv")
	defer unregister()
	d.SetEnvResolver(func(env map[string]string) (map[string]string, error) {
		out := map[string]string{}
		for k, v := range env {
			out[k] = strings.Replace(v, "vault:abc", "plaintext", 1)
		}
		return out, nil
	})

	env := map[string]string{"DB_PASSWORD": "vault:abc", "DB_NAME": "app"}
	msg := &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_DeployStack{DeployStack: &agentv1.DeployStackCommand{Env: env}}}
	if err := d.Send("srv", msg); err != nil {
		t.Fatal(err)
	}
	sent := <-ch
	if got := sent.GetDeployStack().GetEnv()["DB_PASSWORD"]; got != "plaintext" {
		t.Errorf("sent DB_PASSWORD = %q", got)
	}
	if got := sent.GetDeployStack().GetEnv()["DB_NAME"]; got != "app" {
		t.Errorf("sent DB_NAME = %q", got)
	}
	if env["DB_PASSWORD"] != "vault:abc" || msg.GetDeployStack().GetEnv()["DB_PASSWORD"] != "vault:abc" {
		t.Error("caller's message was modified")
	}

	// Restore and DeployService carry env too.
	_ = d.Send("srv", &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_Restore{Restore: &agentv1.RestoreCommand{Env: map[string]string{"P": "vault:abc"}}}})
	if got := (<-ch).GetRestore().GetEnv()["P"]; got != "plaintext" {
		t.Errorf("restore env = %q", got)
	}
	_ = d.Send("srv", &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_DeployService{DeployService: &agentv1.DeployServiceCommand{Env: map[string]string{"P": "vault:abc"}}}})
	if got := (<-ch).GetDeployService().GetEnv()["P"]; got != "plaintext" {
		t.Errorf("deploy service env = %q", got)
	}
	// So do an image build's build args.
	_ = d.Send("srv", &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_BuildImage{BuildImage: &agentv1.BuildImageCommand{BuildArgs: map[string]string{"TOKEN": "vault:abc"}}}})
	if got := (<-ch).GetBuildImage().GetBuildArgs()["TOKEN"]; got != "plaintext" {
		t.Errorf("build args = %q", got)
	}
}

func TestConnectionGenerationChangesOnReconnect(t *testing.T) {
	d := NewDispatcher()
	if _, connected := d.Connection("srv"); connected {
		t.Fatal("unregistered server reported connected")
	}
	_, unregister := d.Register("srv")
	first, connected := d.Connection("srv")
	if !connected {
		t.Fatal("registered server reported disconnected")
	}
	unregister()
	if _, connected := d.Connection("srv"); connected {
		t.Fatal("unregistered server still connected")
	}
	_, unregister = d.Register("srv")
	defer unregister()
	if second, _ := d.Connection("srv"); second == first {
		t.Fatal("a reconnect must change the generation")
	}
}

func TestSendRefusesWhenResolutionFails(t *testing.T) {
	d := NewDispatcher()
	ch, unregister := d.Register("srv")
	defer unregister()
	d.SetEnvResolver(func(map[string]string) (map[string]string, error) { return nil, errors.New("secret gone") })

	msg := &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_DeployStack{DeployStack: &agentv1.DeployStackCommand{Env: map[string]string{"P": "vault:x"}}}}
	if err := d.Send("srv", msg); err == nil {
		t.Fatal("Send succeeded with an unresolvable secret")
	}
	select {
	case <-ch:
		t.Fatal("a command with an unresolved reference reached the agent")
	default:
	}
}
