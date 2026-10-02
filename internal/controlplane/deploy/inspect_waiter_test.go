package deploy

import (
	"testing"
	"time"
)

func TestWaiterDeliversToAwaiter(t *testing.T) {
	w := NewWaiter[string]()
	ch, cleanup := w.Await("req-1")
	defer cleanup()

	w.Deliver("req-1", "result")

	select {
	case got := <-ch:
		if got != "result" {
			t.Errorf("got %q, want %q", got, "result")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivered result")
	}
}

func TestWaiterDeliverToUnknownRequestIDIsNoop(t *testing.T) {
	w := NewWaiter[string]()
	// No Await call for "unknown" — must not panic or block.
	w.Deliver("unknown", "ignored")
}

func TestWaiterCleanupUnregisters(t *testing.T) {
	w := NewWaiter[string]()
	ch, cleanup := w.Await("req-1")
	cleanup()

	// Deliver after cleanup must be a no-op (simulating an HTTP handler
	// that gave up/timed out before the agent replied).
	w.Deliver("req-1", "too late")

	select {
	case got, ok := <-ch:
		if ok {
			t.Errorf("received %q after cleanup, want no delivery", got)
		}
	case <-time.After(100 * time.Millisecond):
		// Expected: nothing delivered.
	}
}

func TestWaiterCleanupDoesNotRemoveANewerRegistrationForTheSameID(t *testing.T) {
	w := NewWaiter[string]()
	_, cleanup1 := w.Await("req-1")
	ch2, cleanup2 := w.Await("req-1") // re-registered before cleanup1 runs
	defer cleanup2()

	// Stale cleanup from the first Await must not clobber the second
	// registration — Waiter.Await's doc comment on this exact guard.
	cleanup1()

	w.Deliver("req-1", "for the second waiter")
	select {
	case got := <-ch2:
		if got != "for the second waiter" {
			t.Errorf("got %q, want delivery to the still-registered waiter", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out — stale cleanup incorrectly unregistered the live waiter")
	}
}

func TestWaiterOnlyBuffersOneResult(t *testing.T) {
	w := NewWaiter[int]()
	ch, cleanup := w.Await("req-1")
	defer cleanup()

	// Deliver is called at most once per request in practice, but the
	// buffered-size-1 channel plus non-blocking send means a second
	// Deliver before the first is drained must not block or panic.
	w.Deliver("req-1", 1)
	w.Deliver("req-1", 2)

	got := <-ch
	if got != 1 {
		t.Errorf("got %d, want the first delivered value 1", got)
	}
}
