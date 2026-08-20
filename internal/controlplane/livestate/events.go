// Package livestate fans out each server's latest heartbeat (resources +
// container inventory) to whatever REST clients are currently watching that
// server's live status stream. Structurally mirrors
// internal/controlplane/deploy's EventBus, but kept separate: this isn't
// deployment orchestration, and the payload — a live gauge of current
// state, not an append-only event log — has its own shape and lifecycle.
package livestate

import (
	"sync"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// ServerUpdate is the live snapshot pushed to a server's status stream on
// every heartbeat.
type ServerUpdate struct {
	Resources  store.ResourceSnapshot `json:"resources"`
	Containers []store.ContainerState `json:"containers"`
	UpdatedAt  time.Time              `json:"updatedAt"`
}

// EventBus fans out ServerUpdates to subscribers of a given server_id.
type EventBus struct {
	mu          sync.Mutex
	subscribers map[string][]chan ServerUpdate
}

func NewEventBus() *EventBus {
	return &EventBus{subscribers: make(map[string][]chan ServerUpdate)}
}

// Subscribe returns a channel that receives every update Published for
// serverID from now on. The caller must call the returned unsubscribe func
// when done watching.
func (b *EventBus) Subscribe(serverID string) (ch chan ServerUpdate, unsubscribe func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch = make(chan ServerUpdate, 8)
	b.subscribers[serverID] = append(b.subscribers[serverID], ch)

	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subs := b.subscribers[serverID]
		for i, c := range subs {
			if c == ch {
				b.subscribers[serverID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(b.subscribers[serverID]) == 0 {
			delete(b.subscribers, serverID)
		}
	}
}

// Publish delivers update to every current subscriber of serverID.
// Non-blocking: a subscriber whose channel is full (a slow/stalled client)
// misses the update rather than stalling heartbeat processing.
func (b *EventBus) Publish(serverID string, update ServerUpdate) {
	b.mu.Lock()
	subs := append([]chan ServerUpdate(nil), b.subscribers[serverID]...)
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- update:
		default:
		}
	}
}
