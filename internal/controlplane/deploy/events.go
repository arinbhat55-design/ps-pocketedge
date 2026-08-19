package deploy

import (
	"sync"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// EventBus fans out deployment_events to whatever REST clients are
// currently watching a given deployment over its WebSocket status stream —
// this is what makes that stream push-driven rather than the client having
// to poll, and what makes it push-driven from the control plane's side too
// (no internal polling loop against the database).
type EventBus struct {
	mu          sync.Mutex
	subscribers map[string][]chan store.DeploymentEvent
}

func NewEventBus() *EventBus {
	return &EventBus{subscribers: make(map[string][]chan store.DeploymentEvent)}
}

// Subscribe returns a channel that receives every event Published for
// deploymentID from now on. The caller must call the returned unsubscribe
// func when done watching.
func (b *EventBus) Subscribe(deploymentID string) (ch chan store.DeploymentEvent, unsubscribe func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch = make(chan store.DeploymentEvent, 8)
	b.subscribers[deploymentID] = append(b.subscribers[deploymentID], ch)

	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subs := b.subscribers[deploymentID]
		for i, c := range subs {
			if c == ch {
				b.subscribers[deploymentID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(b.subscribers[deploymentID]) == 0 {
			delete(b.subscribers, deploymentID)
		}
	}
}

// Publish delivers event to every current subscriber of its deployment.
// Non-blocking: a subscriber whose channel is full (i.e. a slow/stalled
// client) misses the event rather than stalling the deploy pipeline.
func (b *EventBus) Publish(deploymentID string, event store.DeploymentEvent) {
	b.mu.Lock()
	subs := append([]chan store.DeploymentEvent(nil), b.subscribers[deploymentID]...)
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
}
