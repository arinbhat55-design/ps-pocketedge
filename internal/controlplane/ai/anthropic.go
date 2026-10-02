// Package ai provides AI-assisted log summarization and root-cause
// suggestions for the container troubleshooting view, backed by the
// Anthropic Messages API.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// model is fixed rather than configurable — this feature is a bounded,
// well-defined summarization task, not something that needs per-deployment
// model tuning.
const model = "claude-opus-5"

// maxLogChars bounds how much log text is sent per request — a runaway
// container's log dump shouldn't blow past the model's context window or
// balloon the request's token cost. Truncates from the front, keeping the
// most recent (most relevant to "what's wrong right now") lines.
const maxLogChars = 40000

// ErrNotConfigured is returned by every Client method when
// ANTHROPIC_API_KEY isn't set — callers check this to return a clear 501
// to the Flutter app rather than a generic 500.
var ErrNotConfigured = errors.New("AI features are not configured: set ANTHROPIC_API_KEY on the control plane")

// Client wraps the Anthropic Messages API for log analysis. A nil *Client
// (see New) means the API key isn't configured; every method treats that
// as ErrNotConfigured rather than every call site needing its own
// "is AI enabled" check.
type Client struct {
	sdk anthropic.Client
}

// New returns a Client if ANTHROPIC_API_KEY is set in the environment,
// nil otherwise.
func New() *Client {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil
	}
	return &Client{sdk: anthropic.NewClient()}
}

// Configured reports whether AI features are available — used by the API
// layer to answer "should the Flutter app show the AI-summary button" up
// front rather than only failing on click.
func (c *Client) Configured() bool { return c != nil }

// Analysis is the AI-assisted summary + root-cause result for one
// container's recent logs.
type Analysis struct {
	Summary        string `json:"summary"`
	RootCause      string `json:"rootCause"`
	Recommendation string `json:"recommendation"`
}

// AnalyzeLogs asks Claude to summarize logText (a container's recent
// combined stdout/stderr, already assembled by the caller from
// StreamLogsCommand's chunks) and suggest a likely root cause for any
// failures visible in it, given containerContext (image, current health
// status, restart count, recent events — whatever the caller has on hand)
// as background.
func (c *Client) AnalyzeLogs(ctx context.Context, containerContext, logText string) (Analysis, error) {
	if c == nil {
		return Analysis{}, ErrNotConfigured
	}

	if len(logText) > maxLogChars {
		logText = logText[len(logText)-maxLogChars:]
	}

	prompt := "You are helping an operator troubleshoot a Docker container from a fleet-management dashboard.\n\n" +
		"Container context:\n" + containerContext + "\n\n" +
		"Recent combined stdout/stderr (may be truncated to the most recent portion):\n" + logText + "\n\n" +
		"Reply with ONLY a JSON object, no surrounding prose or code fence, of the exact shape " +
		`{"summary": "...", "rootCause": "...", "recommendation": "..."}.` + "\n" +
		"summary: 2-3 sentences describing what these logs show. " +
		`rootCause: your best-guess root cause of any errors/failures visible, or "No issues found in the available logs." if the logs look healthy. ` +
		"recommendation: one concrete, actionable next step for the operator."

	resp, err := c.sdk.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return Analysis{}, err
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	var out Analysis
	if err := json.Unmarshal([]byte(extractJSON(text.String())), &out); err != nil {
		// The model didn't return clean JSON despite the instruction —
		// fall back to the raw text as the summary rather than failing
		// the whole request over a formatting slip.
		return Analysis{Summary: text.String()}, nil
	}
	return out, nil
}

// extractJSON trims any leading/trailing text around a JSON object,
// defensive against the model wrapping its answer in a code fence or a
// sentence despite the "ONLY a JSON object" instruction.
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}
