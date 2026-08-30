package deploy

import (
	"sync"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// InspectWaiter correlates an InspectContainerCommand sent to an agent
// (from an HTTP handler goroutine) with the ContainerDetail reply that
// arrives later on grpcserver.Session's receive loop — a different
// goroutine, since the AgentSession stream is agent-initiated and only
// that loop ever calls stream.Recv(). Unlike EventBus, this is a single
// request/reply per request_id, not a fan-out subscription.
type InspectWaiter struct {
	mu      sync.Mutex
	waiters map[string]chan *agentv1.ContainerDetail
}

func NewInspectWaiter() *InspectWaiter {
	return &InspectWaiter{waiters: make(map[string]chan *agentv1.ContainerDetail)}
}

// Await registers requestID and returns a buffered (size 1) channel that
// receives the matching ContainerDetail if Deliver is called before the
// caller gives up. The caller must call cleanup exactly once (typically
// deferred) to unregister, whether or not a reply arrived.
func (w *InspectWaiter) Await(requestID string) (ch chan *agentv1.ContainerDetail, cleanup func()) {
	ch = make(chan *agentv1.ContainerDetail, 1)

	w.mu.Lock()
	w.waiters[requestID] = ch
	w.mu.Unlock()

	return ch, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.waiters[requestID] == ch {
			delete(w.waiters, requestID)
		}
	}
}

// Deliver hands result to requestID's waiter if one is currently
// registered. A reply for an unknown/already-timed-out/already-delivered
// request_id is silently dropped — the HTTP request that asked for it has
// already given up and returned an error to its caller.
func (w *InspectWaiter) Deliver(requestID string, result *agentv1.ContainerDetail) {
	w.mu.Lock()
	ch, ok := w.waiters[requestID]
	w.mu.Unlock()
	if !ok {
		return
	}

	select {
	case ch <- result:
	default:
	}
}
