package deploy

import (
	"sync"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// StreamRelay is Waiter's multi-delivery counterpart: a log tail or exec
// session sends many AgentMessages for one request_id (until it reports
// done), instead of Waiter's exactly-one reply, so the HTTP/WebSocket
// handler relaying it onward needs a subscription, not a single Await.
type StreamRelay[T any] struct {
	mu   sync.Mutex
	subs map[string]chan T
}

func NewStreamRelay[T any]() *StreamRelay[T] {
	return &StreamRelay[T]{subs: make(map[string]chan T)}
}

// Subscribe returns a channel that receives every message Published for
// requestID from now on. The caller must call unsubscribe when done
// (typically once a "done" message arrives, or the client disconnects).
func (r *StreamRelay[T]) Subscribe(requestID string) (ch chan T, unsubscribe func()) {
	ch = make(chan T, 64)

	r.mu.Lock()
	r.subs[requestID] = ch
	r.mu.Unlock()

	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.subs[requestID] == ch {
			delete(r.subs, requestID)
		}
	}
}

// Publish delivers msg to requestID's subscriber, if one is currently
// registered. Non-blocking, same as EventBus.Publish: a subscriber that
// isn't draining fast enough misses the message rather than stalling the
// grpcserver receive loop that calls this.
func (r *StreamRelay[T]) Publish(requestID string, msg T) {
	r.mu.Lock()
	ch, ok := r.subs[requestID]
	r.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

// LogStreamRelay fans out LogChunk messages to whichever handler is
// relaying a given log-tail request_id onward (a live WS tail, or a
// bounded collect-until-done for download/AI-analysis).
type LogStreamRelay = StreamRelay[*agentv1.LogChunk]

func NewLogStreamRelay() *LogStreamRelay { return NewStreamRelay[*agentv1.LogChunk]() }

// ExecStreamRelay fans out ExecOutputChunk messages to the WebSocket
// handler relaying one interactive exec session.
type ExecStreamRelay = StreamRelay[*agentv1.ExecOutputChunk]

func NewExecStreamRelay() *ExecStreamRelay { return NewStreamRelay[*agentv1.ExecOutputChunk]() }

// EventListWaiter correlates a ListEventsCommand with its EventListResult
// reply — a normal one-shot Waiter, since events are a bounded historical
// fetch, not a stream.
type EventListWaiter = Waiter[*agentv1.EventListResult]

func NewEventListWaiter() *EventListWaiter { return NewWaiter[*agentv1.EventListResult]() }
