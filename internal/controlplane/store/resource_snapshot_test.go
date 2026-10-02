package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResourceSnapshotJSONDistinguishesZeroFromUnknown(t *testing.T) {
	zero := uint64(0)
	used := uint64(123)
	raw, err := json.Marshal(ResourceSnapshot{UsedMemoryBytes: &used, UsedDiskBytes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"usedMemoryBytes":123`) || !strings.Contains(string(raw), `"usedDiskBytes":0`) {
		t.Fatal(string(raw))
	}
	var decoded ResourceSnapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.UsedDiskBytes == nil || *decoded.UsedDiskBytes != 0 {
		t.Fatal("lost valid zero usage")
	}
	// Unmarshal into a fresh value, as API/store readers do.
	var old ResourceSnapshot
	if err := json.Unmarshal([]byte(`{"cpuPercent":20}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.UsedMemoryBytes != nil || old.UsedDiskBytes != nil {
		t.Fatal("old payload invented byte usage")
	}
}
