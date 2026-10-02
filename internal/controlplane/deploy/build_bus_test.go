package deploy

import (
	"testing"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func TestBuildBusDeliversToEverySubscriberOfTheBuild(t *testing.T) {
	bus := NewBuildBus()
	a, unsubA := bus.Subscribe("b1")
	defer unsubA()
	b, unsubB := bus.Subscribe("b1")
	other, unsubOther := bus.Subscribe("b2")
	defer unsubOther()

	bus.Publish(&agentv1.BuildStatus{BuildId: "b1", Log: "hello"})
	for _, ch := range []chan *agentv1.BuildStatus{a, b} {
		if got := (<-ch).GetLog(); got != "hello" {
			t.Fatalf("got %q", got)
		}
	}
	select {
	case <-other:
		t.Fatal("another build's subscriber got the status")
	default:
	}

	unsubB()
	bus.Publish(&agentv1.BuildStatus{BuildId: "b1"})
	<-a
	select {
	case <-b:
		t.Fatal("unsubscribed channel still receives")
	default:
	}
}
