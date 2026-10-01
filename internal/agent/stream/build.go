package stream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	dockerclient "github.com/docker/docker/client"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/build"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

const (
	// buildLogFlushInterval/buildLogFlushBytes batch build output into
	// BuildStatus messages: often enough to follow live, without one
	// message per line of a noisy `npm install`.
	buildLogFlushInterval = 500 * time.Millisecond
	buildLogFlushBytes    = 32 << 10
	// maxBuildLogBytes caps the output sent for one build.
	maxBuildLogBytes = 4 << 20
)

// handleBuildImage builds cmd's image, reporting progress and output as
// BuildStatus messages. A StopStreamCommand with the build's ID cancels it.
func (r *Runner) handleBuildImage(sessionCtx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.BuildImageCommand, outbound chan<- *agentv1.AgentMessage, streams *activeStreams) {
	ctx, cancel := context.WithCancel(sessionCtx)
	defer cancel()
	done := streams.registerTask(cmd.GetBuildId(), cancel)
	defer done()

	logs := newBuildLog(sessionCtx, cmd.GetBuildId(), outbound)
	defer logs.close()

	if !r.AllowBuilds {
		logs.finish(agentv1.BuildPhase_BUILD_PHASE_FAILED, "image builds are disabled on this server — set allow_builds: true in its agent config (or pass -allow-builds) and restart the agent", nil)
		return
	}
	if dockerCli == nil {
		logs.finish(agentv1.BuildPhase_BUILD_PHASE_FAILED, "docker client unavailable on this agent", nil)
		return
	}

	select {
	case r.buildSlot <- struct{}{}:
		logs.phase(agentv1.BuildPhase_BUILD_PHASE_QUEUED, "build started")
	default:
		logs.phase(agentv1.BuildPhase_BUILD_PHASE_QUEUED, "waiting for another build on this server to finish")
		select {
		case r.buildSlot <- struct{}{}:
		case <-ctx.Done():
			logs.finish(agentv1.BuildPhase_BUILD_PHASE_CANCELLED, "build cancelled", nil)
			return
		}
	}
	defer func() { <-r.buildSlot }()

	workDir := r.BuildDir
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "pspocketedge-builds")
	}
	r.log.Info("building image", "build_id", cmd.GetBuildId(), "deployment_id", cmd.GetDeploymentId(), "tag", cmd.GetImageTag(), "commit", cmd.GetGitCommit())
	res, err := build.Run(ctx, dockerCli, cmd, build.Options{WorkDir: workDir}, logs.phase, logs)
	switch {
	case err != nil && errors.Is(ctx.Err(), context.Canceled) && sessionCtx.Err() == nil:
		r.log.Info("build cancelled", "build_id", cmd.GetBuildId())
		logs.finish(agentv1.BuildPhase_BUILD_PHASE_CANCELLED, "build cancelled", nil)
	case err != nil:
		r.log.Error("build failed", "build_id", cmd.GetBuildId(), "error", err)
		logs.finish(agentv1.BuildPhase_BUILD_PHASE_FAILED, err.Error(), nil)
	default:
		r.log.Info("build succeeded", "build_id", cmd.GetBuildId(), "image_id", res.ImageID, "reused", res.Reused)
		message := "image built"
		if res.Reused {
			message = "image already built — reused"
		}
		logs.finish(agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED, message, res)
	}
}

// buildLog is the io.Writer build output goes to. It batches output and
// sends it, with the build's current phase, as BuildStatus messages.
type buildLog struct {
	ctx      context.Context
	buildID  string
	outbound chan<- *agentv1.AgentMessage

	mu        sync.Mutex
	buf       []byte
	seq       int64
	sent      int
	truncated bool
	current   agentv1.BuildPhase
	stop      chan struct{}
	stopped   sync.Once
}

func newBuildLog(ctx context.Context, buildID string, outbound chan<- *agentv1.AgentMessage) *buildLog {
	l := &buildLog{ctx: ctx, buildID: buildID, outbound: outbound, current: agentv1.BuildPhase_BUILD_PHASE_QUEUED, stop: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(buildLogFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				l.mu.Lock()
				l.flushLocked()
				l.mu.Unlock()
			case <-l.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return l
}

func (l *buildLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sent+len(l.buf)+len(p) > maxBuildLogBytes {
		if !l.truncated {
			l.truncated = true
			l.buf = append(l.buf, "\n[build output truncated]\n"...)
		}
		return len(p), nil
	}
	l.buf = append(l.buf, p...)
	if len(l.buf) >= buildLogFlushBytes {
		l.flushLocked()
	}
	return len(p), nil
}

// phase sends any pending output, then the phase change.
func (l *buildLog) phase(phase agentv1.BuildPhase, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked()
	l.current = phase
	l.send(&agentv1.BuildStatus{BuildId: l.buildID, Phase: phase, Message: message})
}

// finish sends any pending output and the build's outcome.
func (l *buildLog) finish(phase agentv1.BuildPhase, message string, res *build.Result) {
	l.close()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked()
	l.current = phase
	status := &agentv1.BuildStatus{BuildId: l.buildID, Phase: phase, Message: message}
	if res != nil {
		status.ImageId, status.Reused = res.ImageID, res.Reused
	}
	l.send(status)
}

func (l *buildLog) close() {
	l.stopped.Do(func() { close(l.stop) })
}

func (l *buildLog) flushLocked() {
	if len(l.buf) == 0 {
		return
	}
	l.seq++
	l.sent += len(l.buf)
	l.send(&agentv1.BuildStatus{BuildId: l.buildID, Phase: l.current, Log: string(l.buf), LogSeq: l.seq})
	l.buf = nil
}

func (l *buildLog) send(status *agentv1.BuildStatus) {
	select {
	case l.outbound <- &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_BuildStatus{BuildStatus: status}}:
	case <-l.ctx.Done():
	}
}
