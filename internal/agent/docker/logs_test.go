package docker

import (
	"testing"
	"time"
)

func TestParseLogLine(t *testing.T) {
	t.Run("splits a well-formed timestamped line", func(t *testing.T) {
		raw := "2024-01-02T15:04:05.123456789Z hello world"
		line := parseLogLine(raw, "stdout")

		wantTime, err := time.Parse(time.RFC3339Nano, "2024-01-02T15:04:05.123456789Z")
		if err != nil {
			t.Fatalf("test fixture time failed to parse: %v", err)
		}
		if !line.Time.Equal(wantTime) {
			t.Errorf("Time = %v, want %v", line.Time, wantTime)
		}
		if line.Message != "hello world" {
			t.Errorf("Message = %q, want %q", line.Message, "hello world")
		}
		if line.Stream != "stdout" {
			t.Errorf("Stream = %q, want %q", line.Stream, "stdout")
		}
	})

	t.Run("message containing spaces is preserved after the timestamp", func(t *testing.T) {
		raw := "2024-01-02T15:04:05Z   multiple   spaces   here"
		line := parseLogLine(raw, "stderr")
		if line.Message != "  multiple   spaces   here" {
			t.Errorf("Message = %q, want %q", line.Message, "  multiple   spaces   here")
		}
	})

	t.Run("unparseable timestamp falls back to zero time with the whole line as message", func(t *testing.T) {
		raw := "not-a-timestamp some message"
		line := parseLogLine(raw, "stdout")
		if !line.Time.IsZero() {
			t.Errorf("Time = %v, want zero value", line.Time)
		}
		if line.Message != raw {
			t.Errorf("Message = %q, want the whole raw line %q", line.Message, raw)
		}
	})

	t.Run("empty line", func(t *testing.T) {
		line := parseLogLine("", "stdout")
		if !line.Time.IsZero() {
			t.Errorf("Time = %v, want zero value", line.Time)
		}
		if line.Message != "" {
			t.Errorf("Message = %q, want empty", line.Message)
		}
	})

	t.Run("timestamp with no message after it", func(t *testing.T) {
		raw := "2024-01-02T15:04:05Z "
		line := parseLogLine(raw, "stdout")
		if line.Message != "" {
			t.Errorf("Message = %q, want empty", line.Message)
		}
	})
}
