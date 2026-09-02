package deploy

import (
	"sync"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// Waiter correlates a command sent to an agent (from an HTTP handler
// goroutine) with its reply, which arrives later on grpcserver.Session's
// receive loop — a different goroutine, since the AgentSession stream is
// agent-initiated and only that loop ever calls stream.Recv(). Unlike
// EventBus, this is a single request/reply per request_id, not a fan-out
// subscription. Generic so the same correlation logic serves both
// InspectContainerCommand/ContainerDetail and the container lifecycle
// commands/ContainerOpResult.
type Waiter[T any] struct {
	mu      sync.Mutex
	waiters map[string]chan T
}

func NewWaiter[T any]() *Waiter[T] {
	return &Waiter[T]{waiters: make(map[string]chan T)}
}

// Await registers requestID and returns a buffered (size 1) channel that
// receives the matching reply if Deliver is called before the caller gives
// up. The caller must call cleanup exactly once (typically deferred) to
// unregister, whether or not a reply arrived.
func (w *Waiter[T]) Await(requestID string) (ch chan T, cleanup func()) {
	ch = make(chan T, 1)

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
func (w *Waiter[T]) Deliver(requestID string, result T) {
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

// InspectWaiter correlates an InspectContainerCommand with its
// ContainerDetail reply.
type InspectWaiter = Waiter[*agentv1.ContainerDetail]

func NewInspectWaiter() *InspectWaiter { return NewWaiter[*agentv1.ContainerDetail]() }

// OpWaiter correlates a container lifecycle command (action/create/rename/
// clone/recreate/restart-policy update) with its ContainerOpResult reply.
type OpWaiter = Waiter[*agentv1.ContainerOpResult]

func NewOpWaiter() *OpWaiter { return NewWaiter[*agentv1.ContainerOpResult]() }

// ImageListWaiter correlates a ListImagesCommand with its ImageListResult
// reply.
type ImageListWaiter = Waiter[*agentv1.ImageListResult]

func NewImageListWaiter() *ImageListWaiter { return NewWaiter[*agentv1.ImageListResult]() }

// ImageDetailWaiter correlates an InspectImageCommand with its ImageDetail
// reply.
type ImageDetailWaiter = Waiter[*agentv1.ImageDetail]

func NewImageDetailWaiter() *ImageDetailWaiter { return NewWaiter[*agentv1.ImageDetail]() }

// ImageOpWaiter correlates an image lifecycle command (pull/remove/prune)
// with its ImageOpResult reply.
type ImageOpWaiter = Waiter[*agentv1.ImageOpResult]

func NewImageOpWaiter() *ImageOpWaiter { return NewWaiter[*agentv1.ImageOpResult]() }

// NetworkListWaiter correlates a ListNetworksCommand with its
// NetworkListResult reply.
type NetworkListWaiter = Waiter[*agentv1.NetworkListResult]

func NewNetworkListWaiter() *NetworkListWaiter { return NewWaiter[*agentv1.NetworkListResult]() }

// NetworkOpWaiter correlates a network lifecycle command (create/remove/
// connect/disconnect) with its NetworkOpResult reply.
type NetworkOpWaiter = Waiter[*agentv1.NetworkOpResult]

func NewNetworkOpWaiter() *NetworkOpWaiter { return NewWaiter[*agentv1.NetworkOpResult]() }

// VolumeListWaiter correlates a ListVolumesCommand with its VolumeListResult
// reply.
type VolumeListWaiter = Waiter[*agentv1.VolumeListResult]

func NewVolumeListWaiter() *VolumeListWaiter { return NewWaiter[*agentv1.VolumeListResult]() }

// VolumeDetailWaiter correlates an InspectVolumeCommand with its
// VolumeDetail reply.
type VolumeDetailWaiter = Waiter[*agentv1.VolumeDetail]

func NewVolumeDetailWaiter() *VolumeDetailWaiter { return NewWaiter[*agentv1.VolumeDetail]() }

// VolumeOpWaiter correlates a volume lifecycle command (create/remove) with
// its VolumeOpResult reply.
type VolumeOpWaiter = Waiter[*agentv1.VolumeOpResult]

func NewVolumeOpWaiter() *VolumeOpWaiter { return NewWaiter[*agentv1.VolumeOpResult]() }
