package deploy

import (
	"testing"
	"time"
)

func TestStreamRelayDeliversToSubscriber(t *testing.T) {
	relay := NewStreamRelay[string]()
	ch, unsubscribe := relay.Subscribe("req-1")
	defer unsubscribe()

	relay.Publish("req-1", "hello")

	select {
	case msg := <-ch:
		if msg != "hello" {
			t.Errorf("got %q, want %q", msg, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published message")
	}
}

func TestStreamRelaySupportsMultipleDeliveries(t *testing.T) {
	relay := NewStreamRelay[int]()
	ch, unsubscribe := relay.Subscribe("req-1")
	defer unsubscribe()

	relay.Publish("req-1", 1)
	relay.Publish("req-1", 2)
	relay.Publish("req-1", 3)

	for _, want := range []int{1, 2, 3} {
		select {
		case got := <-ch:
			if got != want {
				t.Errorf("got %d, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for message %d", want)
		}
	}
}

func TestStreamRelayPublishToUnknownRequestIDIsNoop(t *testing.T) {
	relay := NewStreamRelay[string]()
	// No subscriber registered for "unknown" — this must not panic or block.
	relay.Publish("unknown", "ignored")
}

func TestStreamRelayUnsubscribeStopsDelivery(t *testing.T) {
	relay := NewStreamRelay[string]()
	ch, unsubscribe := relay.Subscribe("req-1")
	unsubscribe()

	relay.Publish("req-1", "should not arrive")

	select {
	case msg, ok := <-ch:
		if ok {
			t.Errorf("received %q after unsubscribe, want no delivery", msg)
		}
	case <-time.After(100 * time.Millisecond):
		// Expected: nothing delivered.
	}
}

func TestStreamRelayUnsubscribeDoesNotAffectOtherSubscriber(t *testing.T) {
	relay := NewStreamRelay[string]()
	ch1, unsubscribe1 := relay.Subscribe("req-1")
	ch2, unsubscribe2 := relay.Subscribe("req-2")
	defer unsubscribe2()

	unsubscribe1()
	relay.Publish("req-2", "for req-2")

	select {
	case msg := <-ch2:
		if msg != "for req-2" {
			t.Errorf("got %q, want %q", msg, "for req-2")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for req-2's message")
	}

	select {
	case msg, ok := <-ch1:
		if ok {
			t.Errorf("req-1 channel received %q after its own unsubscribe", msg)
		}
	default:
	}
}

func TestStreamRelayPublishNonBlockingWhenSubscriberFull(t *testing.T) {
	relay := NewStreamRelay[int]()
	_, unsubscribe := relay.Subscribe("req-1")
	defer unsubscribe()

	// Publish far more than the internal buffer (64) without anyone
	// draining — Publish must never block the caller (grpcserver's single
	// receive loop calls this synchronously).
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			relay.Publish("req-1", i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscriber channel")
	}
}
