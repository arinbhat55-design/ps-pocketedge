package stream

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	dockerclient "github.com/docker/docker/client"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/docker"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// logBatchInterval bounds how often handleStreamLogs flushes buffered
// lines as a LogChunk. Without batching, a fast-logging container under
// follow=true would send one AgentMessage per line onto the shared
// outbound channel (session.go's runSession doc comment), starving
// heartbeats and other commands sharing it.
const logBatchInterval = 200 * time.Millisecond

// handleStreamLogs tails cmd.ContainerId's logs, replying with a sequence
// of LogChunk messages sharing cmd.RequestId until the dump is exhausted
// (follow=false) or a StopStreamCommand for this request_id arrives
// (follow=true, cancelled via streams). Runs in its own goroutine, same as
// every other handler in session.go.
func (r *Runner) handleStreamLogs(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.StreamLogsCommand, outbound chan<- *agentv1.AgentMessage, streams *activeStreams) {
	send := func(chunk *agentv1.LogChunk) {
		select {
		case outbound <- &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_LogChunk{LogChunk: chunk}}:
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		send(&agentv1.LogChunk{RequestId: cmd.GetRequestId(), ContainerId: cmd.GetContainerId(), Done: true, ErrorMessage: "docker client unavailable on this agent"})
		return
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := streams.registerLog(cmd.GetRequestId(), cancel)
	defer done()

	var since, until time.Time
	if s := cmd.GetSinceUnix(); s > 0 {
		since = time.Unix(s, 0)
	}
	if u := cmd.GetUntilUnix(); u > 0 {
		until = time.Unix(u, 0)
	}

	var mu sync.Mutex
	var batch []*agentv1.LogLine
	flush := func() {
		mu.Lock()
		if len(batch) == 0 {
			mu.Unlock()
			return
		}
		lines := batch
		batch = nil
		mu.Unlock()
		send(&agentv1.LogChunk{RequestId: cmd.GetRequestId(), ContainerId: cmd.GetContainerId(), Lines: lines})
	}

	flusherDone := make(chan struct{})
	go func() {
		defer close(flusherDone)
		ticker := time.NewTicker(logBatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				flush()
			}
		}
	}()

	streamErr := docker.StreamLogs(streamCtx, dockerCli, cmd.GetContainerId(), cmd.GetFollow(), int(cmd.GetTailLines()), since, until, func(line docker.LogLine) {
		ts := int64(0)
		if !line.Time.IsZero() {
			ts = line.Time.UnixNano()
		}
		mu.Lock()
		batch = append(batch, &agentv1.LogLine{TimestampUnixNano: ts, Stream: line.Stream, Message: line.Message})
		mu.Unlock()
	})

	cancel()
	<-flusherDone
	flush()

	errMsg := ""
	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		errMsg = streamErr.Error()
	}
	send(&agentv1.LogChunk{RequestId: cmd.GetRequestId(), ContainerId: cmd.GetContainerId(), Done: true, ErrorMessage: errMsg})
}

// handleListEvents fetches Docker events for cmd's container (or the
// whole host, if ContainerId is empty) between since/until and replies
// once with the full list, correlated via request_id — a bounded
// historical fetch, same request/reply shape as handleInspectContainer,
// not a live subscription.
func (r *Runner) handleListEvents(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.ListEventsCommand, outbound chan<- *agentv1.AgentMessage) {
	reply := func(result *agentv1.EventListResult) {
		select {
		case outbound <- &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_EventListResult{EventListResult: result}}:
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		reply(&agentv1.EventListResult{RequestId: cmd.GetRequestId(), ErrorMessage: "docker client unavailable on this agent"})
		return
	}

	var since, until time.Time
	if s := cmd.GetSinceUnix(); s > 0 {
		since = time.Unix(s, 0)
	}
	if u := cmd.GetUntilUnix(); u > 0 {
		until = time.Unix(u, 0)
	}

	events, err := docker.ListEvents(ctx, dockerCli, cmd.GetContainerId(), since, until)
	result := &agentv1.EventListResult{RequestId: cmd.GetRequestId()}
	if err != nil {
		result.ErrorMessage = err.Error()
	}
	result.Events = make([]*agentv1.ContainerEvent, len(events))
	for i, e := range events {
		result.Events[i] = &agentv1.ContainerEvent{
			TimestampUnix: e.Time.Unix(),
			Type:          e.Type,
			Action:        e.Action,
			ContainerId:   e.ContainerID,
			ContainerName: e.ContainerName,
		}
	}
	reply(result)
}

// execReadBufSize is the chunk size handleExecStart reads the pty in —
// large enough that a burst of output (e.g. `ls -la` on a big directory)
// doesn't fragment into many tiny AgentMessages.
const execReadBufSize = 32 * 1024

// handleExecStart opens an interactive exec session inside
// cmd.ContainerId and relays its pty output as a sequence of
// ExecOutputChunk messages sharing cmd.RequestId, until the process exits
// or a StopStreamCommand for this request_id closes the session early
// (via streams). Keystrokes/resizes arrive as ExecInputCommands routed
// through streams by session.go's receive loop.
func (r *Runner) handleExecStart(ctx context.Context, dockerCli *dockerclient.Client, cmd *agentv1.ExecStartCommand, outbound chan<- *agentv1.AgentMessage, streams *activeStreams) {
	send := func(chunk *agentv1.ExecOutputChunk) {
		select {
		case outbound <- &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_ExecOutput{ExecOutput: chunk}}:
		case <-ctx.Done():
		}
	}

	if dockerCli == nil {
		send(&agentv1.ExecOutputChunk{RequestId: cmd.GetRequestId(), Done: true, ErrorMessage: "docker client unavailable on this agent"})
		return
	}

	session, err := docker.StartExec(ctx, dockerCli, cmd.GetContainerId(), cmd.GetCmd(), uint(cmd.GetCols()), uint(cmd.GetRows()))
	if err != nil {
		send(&agentv1.ExecOutputChunk{RequestId: cmd.GetRequestId(), Done: true, ErrorMessage: err.Error()})
		return
	}
	defer session.Conn.Close()

	execCtx, execCancel := context.WithCancel(ctx)
	defer execCancel()
	input, unregister := streams.registerExec(cmd.GetRequestId(), func() {
		execCancel()
		session.Conn.Close()
	})
	defer unregister()

	go func() {
		for {
			select {
			case <-execCtx.Done():
				return
			case in := <-input:
				if in == nil {
					continue
				}
				if in.GetResizeCols() > 0 && in.GetResizeRows() > 0 {
					_ = session.Resize(ctx, uint(in.GetResizeCols()), uint(in.GetResizeRows()))
					continue
				}
				if len(in.GetData()) > 0 {
					if _, err := session.Conn.Write(in.GetData()); err != nil {
						return
					}
				}
			}
		}
	}()

	buf := make([]byte, execReadBufSize)
	var readErr error
	for {
		n, err := session.Conn.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			send(&agentv1.ExecOutputChunk{RequestId: cmd.GetRequestId(), Data: chunk})
		}
		if err != nil {
			readErr = err
			break
		}
	}

	exitCode, _ := session.ExitCode(ctx)
	errMsg := ""
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		errMsg = readErr.Error()
	}
	send(&agentv1.ExecOutputChunk{RequestId: cmd.GetRequestId(), Done: true, ExitCode: int64(exitCode), ErrorMessage: errMsg})
}
