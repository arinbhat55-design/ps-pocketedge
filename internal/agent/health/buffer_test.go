package health

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBufferPreservesCapacityAndZeroUsage(t *testing.T) {
	usedMemory, usedDisk := uint64(1024), uint64(0)
	buffer := Open(filepath.Join(t.TempDir(), "samples.jsonl"), 10)
	if err := buffer.Append(Sample{RecordedAt: time.Now(), TotalMemoryBytes: 2048, NumCPUs: 4,
		TotalDiskBytes: 4096, UsedMemoryBytes: &usedMemory, UsedDiskBytes: &usedDisk}); err != nil {
		t.Fatal(err)
	}
	samples, err := buffer.DrainAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 {
		t.Fatal(samples)
	}
	s := samples[0]
	if s.TotalMemoryBytes != 2048 || s.TotalDiskBytes != 4096 || s.NumCPUs != 4 ||
		s.UsedMemoryBytes == nil || *s.UsedMemoryBytes != usedMemory || s.UsedDiskBytes == nil || *s.UsedDiskBytes != 0 {
		t.Fatalf("lost resource fields: %+v", s)
	}
}
