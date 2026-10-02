// Package state persists the agent's identity (server ID + long-lived
// credential) to disk across restarts, so a systemd-managed agent doesn't
// try to re-enroll with an already-consumed one-time token every time it
// restarts.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Identity struct {
	ServerID   string `json:"server_id"`
	Credential string `json:"credential"`
}

// Load reads a previously saved Identity from path. Returns (nil, nil) if
// no state file exists yet — that's the normal "never enrolled" case, not
// an error.
func Load(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, err
	}
	return &id, nil
}

// Save writes id to path, creating parent directories if needed.
func Save(path string, id *Identity) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(id)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
