package stream

import (
	"testing"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func TestActiveStreamsStopCallsRegisteredCancel(t *testing.T) {
	streams := newActiveStreams()
	called := false
	done := streams.registerLog("req-1", func() { called = true })
	defer done()

	streams.stop("req-1")

	if !called {
		t.Error("stop() did not call the registered cancel func")
	}
}

func TestActiveStreamsStopOnUnknownRequestIDIsNoop(t *testing.T) {
	streams := newActiveStreams()
	// No registration for "unknown" — must not panic.
	streams.stop("unknown")
}

func TestActiveStreamsStopAfterDoneIsNoop(t *testing.T) {
	streams := newActiveStreams()
	calls := 0
	done := streams.registerLog("req-1", func() { calls++ })
	done() // simulates the log handler's own goroutine finishing normally

	streams.stop("req-1")

	if calls != 0 {
		t.Errorf("stop() after done() called cancel %d times, want 0", calls)
	}
}

func TestActiveStreamsRegisterExecReturnsWorkingInputChannel(t *testing.T) {
	streams := newActiveStreams()
	input, done := streams.registerExec("req-1", func() {})
	defer done()

	cmd := &agentv1.ExecInputCommand{RequestId: "req-1", Data: []byte("ls\n")}
	streams.sendInput(cmd)

	select {
	case got := <-input:
		if string(got.GetData()) != "ls\n" {
			t.Errorf("got data %q, want %q", got.GetData(), "ls\n")
		}
	default:
		t.Fatal("sendInput did not deliver to the registered exec session's input channel")
	}
}

func TestActiveStreamsSendInputToUnknownRequestIDIsNoop(t *testing.T) {
	streams := newActiveStreams()
	// No registration at all — must not panic or block.
	streams.sendInput(&agentv1.ExecInputCommand{RequestId: "unknown", Data: []byte("x")})
}

func TestActiveStreamsSendInputRoutesOnlyToMatchingRequestID(t *testing.T) {
	streams := newActiveStreams()
	inputA, doneA := streams.registerExec("req-a", func() {})
	defer doneA()
	inputB, doneB := streams.registerExec("req-b", func() {})
	defer doneB()

	streams.sendInput(&agentv1.ExecInputCommand{RequestId: "req-a", Data: []byte("for-a")})

	select {
	case got := <-inputA:
		if string(got.GetData()) != "for-a" {
			t.Errorf("inputA got %q, want %q", got.GetData(), "for-a")
		}
	default:
		t.Fatal("expected req-a's channel to receive the input")
	}

	select {
	case got := <-inputB:
		t.Errorf("inputB unexpectedly received %q — sendInput must not cross-deliver", got.GetData())
	default:
		// Expected: nothing delivered to the other session.
	}
}

func TestActiveStreamsStopOnExecCallsItsOwnCancelNotAnotherSessions(t *testing.T) {
	streams := newActiveStreams()
	var stoppedA, stoppedB bool
	_, doneA := streams.registerExec("req-a", func() { stoppedA = true })
	defer doneA()
	_, doneB := streams.registerExec("req-b", func() { stoppedB = true })
	defer doneB()

	streams.stop("req-a")

	if !stoppedA {
		t.Error("stop(\"req-a\") did not call req-a's cancel func")
	}
	if stoppedB {
		t.Error("stop(\"req-a\") incorrectly called req-b's cancel func")
	}
}

func TestActiveStreamsRemoveIsIdempotent(t *testing.T) {
	streams := newActiveStreams()
	done := streams.registerLog("req-1", func() {})
	done()
	done() // calling twice must not panic (defer + explicit call is a common pattern)
}
