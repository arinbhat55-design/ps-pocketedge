// Package version holds build-time version info, injected via -ldflags.
package version

var (
	// Version is the semantic version, set at build time (e.g. "v0.1.0").
	Version = "dev"
	// Commit is the git commit SHA, set at build time.
	Commit = "none"
	// BuildDate is the build timestamp, set at build time.
	BuildDate = "unknown"
)

// String returns a human-readable version string.
func String() string {
	return Version + " (" + Commit + ", " + BuildDate + ")"
}
