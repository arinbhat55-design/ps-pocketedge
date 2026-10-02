package stream

import (
	"testing"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/health"
	"google.golang.org/protobuf/proto"
)

func TestHeartbeatPreservesUsagePresence(t *testing.T) {
	usedMemory, usedDisk := uint64(1234), uint64(0)
	snap := health.Snapshot{TotalMemoryBytes: 4096, TotalDiskBytes: 8192,
		UsedMemoryBytes: &usedMemory, UsedDiskBytes: &usedDisk}
	message := buildHeartbeat("server", time.Now(), snap, nil, false, nil, false)
	raw, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	decoded := message.ProtoReflect().New().Interface()
	if err := proto.Unmarshal(raw, decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(message, decoded) {
		t.Fatal("heartbeat changed in protobuf round trip")
	}
	resources := message.GetHeartbeat().GetResources()
	if resources.GetUsedMemoryBytes() != usedMemory || resources.UsedDiskBytes == nil {
		t.Fatal("missing usage bytes, including valid zero disk usage")
	}
	old := buildHeartbeat("server", time.Now(), health.Snapshot{}, nil, false, nil, false)
	if old.GetHeartbeat().GetResources().UsedMemoryBytes != nil {
		t.Fatal("unknown usage became zero usage")
	}
}
