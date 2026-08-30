package health

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

// Sample is one buffered resource-usage reading, taken while the agent was
// disconnected from the control plane and replayed (with its original
// RecordedAt) once reconnected — see Buffer.
type Sample struct {
	RecordedAt  time.Time `json:"recordedAt"`
	CPUPercent  float64   `json:"cpuPercent"`
	MemPercent  float64   `json:"memPercent"`
	DiskPercent float64   `json:"diskPercent"`
}

// Buffer is a bounded on-disk ring buffer of Samples, stored as JSON lines.
// It holds no open file handle between calls — each Append/DrainAll opens,
// does its work, and closes — so it's safe across agent restarts and never
// leaks a descriptor if the agent process is killed mid-outage.
type Buffer struct {
	path       string
	maxEntries int
}

// Open returns a Buffer backed by path, capped at maxEntries (oldest
// dropped first). It does not touch the filesystem yet — a missing file is
// simply treated as empty by Append/DrainAll.
func Open(path string, maxEntries int) *Buffer {
	return &Buffer{path: path, maxEntries: maxEntries}
}

// Append adds s to the buffer, dropping the oldest entries past maxEntries.
func (b *Buffer) Append(s Sample) error {
	samples, err := b.readAll()
	if err != nil {
		return err
	}
	samples = append(samples, s)
	if len(samples) > b.maxEntries {
		samples = samples[len(samples)-b.maxEntries:]
	}
	return b.writeAll(samples)
}

// DrainAll returns every buffered sample, oldest first, and clears the
// buffer. Clearing before the caller has necessarily delivered every
// sample means a crash mid-flush can drop the on-disk copy of ones not yet
// sent — an accepted at-least-effort tradeoff for monitoring data, not a
// ledger (see the edge-device tuning plan's risk notes).
func (b *Buffer) DrainAll() ([]Sample, error) {
	samples, err := b.readAll()
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, nil
	}
	if err := os.Remove(b.path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return samples, nil
}

func (b *Buffer) readAll() ([]Sample, error) {
	f, err := os.Open(b.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var samples []Sample
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var s Sample
		if err := json.Unmarshal(scanner.Bytes(), &s); err != nil {
			continue // skip a corrupt line rather than losing the whole buffer
		}
		samples = append(samples, s)
	}
	return samples, scanner.Err()
}

func (b *Buffer) writeAll(samples []Sample) error {
	f, err := os.OpenFile(b.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, s := range samples {
		if err := enc.Encode(s); err != nil {
			return err
		}
	}
	return nil
}
