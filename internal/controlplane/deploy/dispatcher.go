// Package deploy routes deployment commands to the correct connected
// agent. The AgentSession gRPC stream is agent-initiated (the agent dials
// out, not the control plane), so once a REST handler decides "send this
// stack to server X" it needs a way to reach that specific agent's
// already-open stream — that's what Dispatcher provides.
package deploy

import (
	"errors"
	"sync"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// ErrAgentNotConnected means the target server has no open Session stream
// right now (never enrolled-and-connected, or currently offline).
var ErrAgentNotConnected = errors.New("agent not connected")

// Dispatcher tracks one outbound channel per currently-connected agent.
type Dispatcher struct {
	mu       sync.Mutex
	channels map[string]chan *agentv1.ControlMessage
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{channels: make(map[string]chan *agentv1.ControlMessage)}
}

// Register creates the outbound channel for serverID's Session stream.
// The caller (grpcserver.Session) must call the returned unregister func
// when the stream ends. If serverID was already registered (e.g. a stale
// connection that hasn't been cleaned up yet), the new registration
// replaces it.
func (d *Dispatcher) Register(serverID string) (ch chan *agentv1.ControlMessage, unregister func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	ch = make(chan *agentv1.ControlMessage, 8)
	d.channels[serverID] = ch

	return ch, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		// Only remove if it's still our channel — a newer connection for
		// the same server may have already replaced it.
		if d.channels[serverID] == ch {
			delete(d.channels, serverID)
		}
	}
}

// IsConnected reports whether serverID currently has an open Session stream.
func (d *Dispatcher) IsConnected(serverID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.channels[serverID]
	return ok
}

// Send delivers msg to serverID's active Session stream. Returns
// ErrAgentNotConnected if the agent has no open stream right now.
func (d *Dispatcher) Send(serverID string, msg *agentv1.ControlMessage) error {
	d.mu.Lock()
	ch, ok := d.channels[serverID]
	d.mu.Unlock()
	if !ok {
		return ErrAgentNotConnected
	}

	select {
	case ch <- msg:
		return nil
	default:
		return errors.New("agent command queue full")
	}
}
