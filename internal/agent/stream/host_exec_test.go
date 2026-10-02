package stream

import (
	"context"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func TestHostExecRelaysQueuedInputWithoutDocker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outbound := make(chan *agentv1.AgentMessage, 32)
	streams := newActiveStreams()
	runner := &Runner{}
	runner.handleExecStart(ctx, nil, &agentv1.ExecStartCommand{
		RequestId: "host-test", HostShell: true, Cmd: []string{"/bin/sh", "-c", "read value; printf 'host:%s' \"$value\"; exit 7"},
	}, outbound, streams)
	streams.sendInput(&agentv1.ExecInputCommand{RequestId: "host-test", Data: []byte("hello\n")})
	var output strings.Builder
	for {
		select {
		case message := <-outbound:
			chunk := message.GetExecOutput()
			output.Write(chunk.GetData())
			if chunk.GetDone() {
				if chunk.GetErrorMessage() != "" || chunk.GetExitCode() != 7 || !strings.Contains(output.String(), "host:hello") {
					t.Fatalf("completion: %v output: %q", chunk, output.String())
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("host terminal did not complete")
		}
	}
}

func TestHostExecRequiresExplicitTarget(t *testing.T) {
	for _, command := range []*agentv1.ExecStartCommand{
		{RequestId: "missing-target"},
		{RequestId: "mixed-target", HostShell: true, ContainerId: "container"},
	} {
		t.Run(command.RequestId, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			outbound := make(chan *agentv1.AgentMessage, 1)
			(&Runner{}).handleExecStart(ctx, nil, command, outbound, newActiveStreams())
			select {
			case message := <-outbound:
				chunk := message.GetExecOutput()
				if !chunk.GetDone() || chunk.GetErrorMessage() == "" {
					t.Fatalf("invalid target accepted: %v", chunk)
				}
			case <-ctx.Done():
				t.Fatal("invalid target did not fail promptly")
			}
		})
	}
}
