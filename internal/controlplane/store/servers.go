package store

import (
	"context"
	"encoding/json"
	"time"
)

// Server mirrors a row in the servers table.
type Server struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Hostname        string          `json:"hostname"`
	OS              string          `json:"os"`
	Arch            string          `json:"arch"`
	AgentVersion    string          `json:"agentVersion"`
	Status          string          `json:"status"`
	LastHeartbeatAt *time.Time      `json:"lastHeartbeatAt,omitempty"`
	LastResources   json.RawMessage `json:"lastResources,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
}

// ResourceSnapshot is the shape stored in servers.last_resources.
type ResourceSnapshot struct {
	CPUPercent  float64 `json:"cpuPercent"`
	MemPercent  float64 `json:"memPercent"`
	DiskPercent float64 `json:"diskPercent"`
}

// CreateServer inserts a new server row and returns its generated ID.
// name defaults to hostname at enrollment time; renaming is a later feature.
func (s *Store) CreateServer(ctx context.Context, hostname, osName, arch, agentVersion, agentTokenHash string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO servers (name, hostname, os, arch, agent_version, agent_token_hash, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'online')
		RETURNING id
	`, hostname, hostname, osName, arch, agentVersion, agentTokenHash).Scan(&id)
	return id, err
}

// RecordHeartbeat updates a server's status, last-seen time, and latest
// resource snapshot. last_resources is overwritten (not appended) — see the
// MVP plan's note on why this is a cheap seed for future metrics history,
// not a time-series table.
func (s *Store) RecordHeartbeat(ctx context.Context, serverID string, resources ResourceSnapshot) error {
	payload, err := json.Marshal(resources)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE servers
		SET status = 'online', last_heartbeat_at = now(), last_resources = $2
		WHERE id = $1
	`, serverID, payload)
	return err
}

// ListServers returns all registered servers, most recently created first.
func (s *Store) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, hostname, os, arch, agent_version, status, last_heartbeat_at, last_resources, created_at
		FROM servers
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	servers := []Server{}
	for rows.Next() {
		var sv Server
		if err := rows.Scan(&sv.ID, &sv.Name, &sv.Hostname, &sv.OS, &sv.Arch, &sv.AgentVersion,
			&sv.Status, &sv.LastHeartbeatAt, &sv.LastResources, &sv.CreatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, sv)
	}
	return servers, rows.Err()
}
