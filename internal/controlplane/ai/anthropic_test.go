package ai

import (
	"context"
	"testing"
)

func TestNewReturnsNilWithoutAPIKey(t *testing.T) {
	// t.Setenv restores the pre-test value automatically on cleanup, so
	// this doesn't leak an unset key across other tests in the package.
	t.Setenv("ANTHROPIC_API_KEY", "")

	c := New()
	if c != nil {
		t.Errorf("New() = %v, want nil when ANTHROPIC_API_KEY is unset", c)
	}
}

func TestNewReturnsClientWithAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-key")

	c := New()
	if c == nil {
		t.Fatal("New() = nil, want a non-nil client when ANTHROPIC_API_KEY is set")
	}
	if !c.Configured() {
		t.Error("Configured() = false, want true")
	}
}

func TestNilClientConfigured(t *testing.T) {
	var c *Client
	if c.Configured() {
		t.Error("Configured() on a nil *Client = true, want false")
	}
}

func TestNilClientAnalyzeLogsReturnsErrNotConfigured(t *testing.T) {
	var c *Client
	_, err := c.AnalyzeLogs(context.Background(), "context", "logs")
	if err != ErrNotConfigured {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean JSON object",
			input: `{"summary":"ok"}`,
			want:  `{"summary":"ok"}`,
		},
		{
			name:  "JSON wrapped in a markdown code fence",
			input: "```json\n{\"summary\":\"ok\"}\n```",
			want:  `{"summary":"ok"}`,
		},
		{
			name:  "JSON with leading and trailing prose",
			input: `Sure, here you go: {"summary":"ok"} — hope that helps!`,
			want:  `{"summary":"ok"}`,
		},
		{
			name:  "no braces at all returns the input unchanged",
			input: "no json here",
			want:  "no json here",
		},
		{
			name:  "only a closing brace, no opening one",
			input: "not json }",
			want:  "not json }",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractJSON(tc.input)
			if got != tc.want {
				t.Errorf("extractJSON(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
