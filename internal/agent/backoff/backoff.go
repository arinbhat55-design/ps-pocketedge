// Package backoff holds the agent's one jitter helper, shared by session
// reconnect backoff (internal/agent/stream) and image-pull retry
// (internal/agent/docker) — pulled out to its own package specifically
// because those two packages already have an import relationship
// (stream imports docker), so neither could import the other's copy.
package backoff

import (
	"math/rand"
	"time"
)

// Jitter returns a random duration in [d/2, d] — spreads out retries so
// concurrent attempts (many agents reconnecting, or several image pulls
// retrying) don't all retry in lockstep.
func Jitter(d time.Duration) time.Duration {
	//nolint:gosec // non-cryptographic jitter is fine here
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}
