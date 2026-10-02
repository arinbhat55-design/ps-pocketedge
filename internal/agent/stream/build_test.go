package stream

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func TestHandleBuildImageRefusesWhenBuildsDisabled(t *testing.T) {
	r := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "", "", "", "", "", "", "")
	outbound := make(chan *agentv1.AgentMessage, 4)
	r.handleBuildImage(context.Background(), nil, &agentv1.BuildImageCommand{BuildId: "b1"}, outbound, newActiveStreams())

	status := (<-outbound).GetBuildStatus()
	if status.GetBuildId() != "b1" || status.GetPhase() != agentv1.BuildPhase_BUILD_PHASE_FAILED || !strings.Contains(status.GetMessage(), "allow_builds") {
		t.Fatalf("status = %v", status)
	}
}

func TestBuildLogBatchesOutputInOrder(t *testing.T) {
	outbound := make(chan *agentv1.AgentMessage, 16)
	l := newBuildLog(context.Background(), "b1", outbound)
	l.phase(agentv1.BuildPhase_BUILD_PHASE_BUILDING, "building")
	_, _ = io.WriteString(l, "step 1\n")
	_, _ = io.WriteString(l, "step 2\n")
	l.finish(agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED, "done", nil)

	var statuses []*agentv1.BuildStatus
	timeout := time.After(time.Second)
	for len(statuses) < 3 {
		select {
		case m := <-outbound:
			statuses = append(statuses, m.GetBuildStatus())
		case <-timeout:
			t.Fatalf("got %d statuses", len(statuses))
		}
	}
	if statuses[0].GetPhase() != agentv1.BuildPhase_BUILD_PHASE_BUILDING {
		t.Fatalf("first = %v", statuses[0])
	}
	logMsg := statuses[1]
	if logMsg.GetLog() != "step 1\nstep 2\n" || logMsg.GetLogSeq() != 1 || logMsg.GetPhase() != agentv1.BuildPhase_BUILD_PHASE_BUILDING {
		t.Fatalf("log batch = %v", logMsg)
	}
	if statuses[2].GetPhase() != agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED || statuses[2].GetLog() != "" {
		t.Fatalf("final = %v", statuses[2])
	}
}
