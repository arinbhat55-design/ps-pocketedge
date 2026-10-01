package deploy

import (
	"sync"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// BuildBus fans out BuildStatus messages to everyone following a build:
// the rollout waiting on it to deploy, and any number of dashboards
// streaming its output. Unlike StreamRelay, a build can have many
// subscribers at once.
type BuildBus struct {
	mu   sync.Mutex
	subs map[string][]chan *agentv1.BuildStatus
}

func NewBuildBus() *BuildBus {
	return &BuildBus{subs: make(map[string][]chan *agentv1.BuildStatus)}
}

// Subscribe returns a channel receiving every status Published for
// buildID from now on. The caller must call unsubscribe when done.
func (b *BuildBus) Subscribe(buildID string) (ch chan *agentv1.BuildStatus, unsubscribe func()) {
	ch = make(chan *agentv1.BuildStatus, 256)
	b.mu.Lock()
	b.subs[buildID] = append(b.subs[buildID], ch)
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subs := b.subs[buildID]
		for i, c := range subs {
			if c == ch {
				b.subs[buildID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(b.subs[buildID]) == 0 {
			delete(b.subs, buildID)
		}
	}
}

// Publish delivers status to every current subscriber of its build.
// Non-blocking, like EventBus.Publish: a subscriber that falls behind
// misses messages rather than stalling the agent's receive loop — the
// stored build (status and output) remains the source of truth.
func (b *BuildBus) Publish(status *agentv1.BuildStatus) {
	b.mu.Lock()
	subs := append([]chan *agentv1.BuildStatus(nil), b.subs[status.GetBuildId()]...)
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- status:
		default:
		}
	}
}
