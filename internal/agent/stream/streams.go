package stream

import (
	"sync"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// activeStream is what activeStreams tracks per request_id: stop ends the
// session (cancels a log tail's context, or closes an exec session's pty
// connection — different mechanisms, same effect), and execInput is set
// only for exec sessions, routing keystrokes/resizes to the session's
// writer goroutine.
type activeStream struct {
	stop      func()
	execInput chan *agentv1.ExecInputCommand
}

// activeStreams tracks in-progress log tails and exec sessions by
// request_id for one Session connection. Every other command in
// session.go is one ControlMessage in, one AgentMessage reply out; logs
// (with follow=true) and exec are the two exceptions — a multi-message
// conversation that a later StopStreamCommand or ExecInputCommand must be
// routed into, rather than being dispatched to a fresh handler.
type activeStreams struct {
	mu      sync.Mutex
	entries map[string]*activeStream
}

func newActiveStreams() *activeStreams {
	return &activeStreams{entries: make(map[string]*activeStream)}
}

// registerLog tracks a log-tail goroutine under requestID, stoppable via
// cancel. The returned done func must be called once the goroutine exits
// on its own (e.g. a non-follow dump finishing) to stop tracking it.
func (s *activeStreams) registerLog(requestID string, cancel func()) (done func()) {
	s.mu.Lock()
	s.entries[requestID] = &activeStream{stop: cancel}
	s.mu.Unlock()
	return func() { s.remove(requestID) }
}

// registerExec tracks an exec session under requestID. stop is called to
// end the session early (a StopStreamCommand); the returned channel is
// where ExecInputCommands for this session arrive, and done removes the
// entry once the exec process exits on its own.
func (s *activeStreams) registerExec(requestID string, stop func()) (input chan *agentv1.ExecInputCommand, done func()) {
	// Room for input queued while the exec process starts (piped stdin
	// arrives in up to 4 KiB messages).
	input = make(chan *agentv1.ExecInputCommand, 256)
	s.mu.Lock()
	s.entries[requestID] = &activeStream{stop: stop, execInput: input}
	s.mu.Unlock()
	return input, func() { s.remove(requestID) }
}

// registerTask tracks a long-running one-shot task (an image build) under
// requestID so a StopStreamCommand can cancel it; call done when it ends.
func (s *activeStreams) registerTask(requestID string, cancel func()) (done func()) {
	return s.registerLog(requestID, cancel)
}

func (s *activeStreams) remove(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, requestID)
}

// stop ends the log tail or exec session identified by requestID — a
// no-op if requestID isn't currently active (already finished, or never
// existed, e.g. a client double-clicking "stop").
func (s *activeStreams) stop(requestID string) {
	s.mu.Lock()
	entry, ok := s.entries[requestID]
	s.mu.Unlock()
	if ok && entry.stop != nil {
		entry.stop()
	}
}

// sendInput routes an ExecInputCommand to its session's input channel.
// Silently dropped if the session has already ended or the channel is
// momentarily full (a slow-draining writer goroutine) — losing a resize
// or a keystroke batch under backpressure is preferable to blocking the
// stream's single receive loop.
func (s *activeStreams) sendInput(cmd *agentv1.ExecInputCommand) {
	s.mu.Lock()
	entry, ok := s.entries[cmd.GetRequestId()]
	s.mu.Unlock()
	if !ok || entry.execInput == nil {
		return
	}
	select {
	case entry.execInput <- cmd:
	default:
	}
}
