package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// SuggestedPlan reuses a previous setup, or a running Colima socket. It never
// guesses credentials for unrelated PostgreSQL instances.
func SuggestedPlan(ctx context.Context, home string) Plan {
	p := DefaultPlan(home)
	if marker, err := os.ReadFile(filepath.Join(p.DataPath, ".pspe-setup")); err == nil && string(marker) == "PS-pocketEdge setup v1\n" {
		if b, err := os.ReadFile(filepath.Join(p.DataPath, "plan.json")); err == nil {
			var saved Plan
			if json.Unmarshal(b, &saved) == nil && saved.Validate() == nil {
				return saved
			}
		}
	}
	for _, path := range []string{filepath.Join(home, ".colima/pspocketedge/docker.sock"), filepath.Join(home, ".colima/default/docker.sock")} {
		socket := "unix://" + path
		if _, err := socketVersion(ctx, socket); err == nil {
			p.Runtime = "existing-docker"
			p.Socket = socket
			break
		}
	}
	return p
}
