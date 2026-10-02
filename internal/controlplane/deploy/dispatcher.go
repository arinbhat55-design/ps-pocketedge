// Package deploy routes deployment commands to the correct connected
// agent. The AgentSession gRPC stream is agent-initiated (the agent dials
// out, not the control plane), so once a REST handler decides "send this
// stack to server X" it needs a way to reach that specific agent's
// already-open stream — that's what Dispatcher provides.
package deploy

import (
	"errors"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// ErrAgentNotConnected means the target server has no open Session stream
// right now (never enrolled-and-connected, or currently offline).
var ErrAgentNotConnected = errors.New("agent not connected")

// Dispatcher tracks one outbound channel per currently-connected agent.
type Dispatcher struct {
	mu       sync.Mutex
	channels map[string]chan *agentv1.ControlMessage
	// generations counts registrations per server, so a caller can tell a
	// reconnect (which ends whatever the agent was doing for the old
	// stream) from a connection that never dropped.
	generations map[string]uint64
	resolve     EnvResolver
}

// EnvResolver replaces vault references in a deployment env with the
// secrets' plaintext. It returns env itself when there is nothing to
// resolve.
type EnvResolver func(env map[string]string) (map[string]string, error)

// SetEnvResolver installs the resolver Send applies to every command that
// carries a deployment env (or, for an image build, build args). Resolution happens here, at the last hop
// before the agent, so the rest of the control plane — stored env,
// revisions, API responses, logs — only ever handles references.
func (d *Dispatcher) SetEnvResolver(r EnvResolver) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.resolve = r
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{channels: make(map[string]chan *agentv1.ControlMessage), generations: make(map[string]uint64)}
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
	d.generations[serverID]++

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

// Connection reports whether serverID is connected and which connection it
// is: the generation changes every time the agent reconnects.
func (d *Dispatcher) Connection(serverID string) (generation uint64, connected bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, connected = d.channels[serverID]
	return d.generations[serverID], connected
}

// Send delivers msg to serverID's active Session stream. Returns
// ErrAgentNotConnected if the agent has no open stream right now.
func (d *Dispatcher) Send(serverID string, msg *agentv1.ControlMessage) error {
	d.mu.Lock()
	ch, ok := d.channels[serverID]
	resolve := d.resolve
	d.mu.Unlock()
	if !ok {
		return ErrAgentNotConnected
	}
	if resolve != nil {
		resolved, err := resolveEnv(msg, resolve)
		if err != nil {
			return fmt.Errorf("failed to resolve credentials: %w", err)
		}
		msg = resolved
	}

	select {
	case ch <- msg:
		return nil
	default:
		return errors.New("agent command queue full")
	}
}

// resolveEnv returns msg with its env resolved. The caller's message is
// never modified: when anything is resolved, a clone is sent instead.
func resolveEnv(msg *agentv1.ControlMessage, resolve EnvResolver) (*agentv1.ControlMessage, error) {
	var env map[string]string
	switch p := msg.GetPayload().(type) {
	case *agentv1.ControlMessage_DeployStack:
		env = p.DeployStack.GetEnv()
	case *agentv1.ControlMessage_DeployService:
		env = p.DeployService.GetEnv()
	case *agentv1.ControlMessage_Restore:
		env = p.Restore.GetEnv()
	case *agentv1.ControlMessage_BuildImage:
		env = p.BuildImage.GetBuildArgs()
	default:
		return msg, nil
	}
	if len(env) == 0 {
		return msg, nil
	}
	resolved, err := resolve(env)
	if err != nil {
		return nil, err
	}
	out := proto.Clone(msg).(*agentv1.ControlMessage)
	switch p := out.GetPayload().(type) {
	case *agentv1.ControlMessage_DeployStack:
		p.DeployStack.Env = resolved
	case *agentv1.ControlMessage_DeployService:
		p.DeployService.Env = resolved
	case *agentv1.ControlMessage_Restore:
		p.Restore.Env = resolved
	case *agentv1.ControlMessage_BuildImage:
		p.BuildImage.BuildArgs = resolved
	}
	return out, nil
}
