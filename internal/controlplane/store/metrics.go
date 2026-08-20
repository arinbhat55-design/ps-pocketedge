package store

import (
	"context"
	"time"
)

// MetricSample mirrors a row in server_metric_samples — one point in a
// server's resource-usage history.
type MetricSample struct {
	RecordedAt  time.Time `json:"recordedAt"`
	CPUPercent  float64   `json:"cpuPercent"`
	MemPercent  float64   `json:"memPercent"`
	DiskPercent float64   `json:"diskPercent"`
}

// ContainerState mirrors a row in server_containers — a server's current
// container inventory, replaced in full on every heartbeat (see
// ReplaceContainers).
type ContainerState struct {
	ContainerID  string    `json:"containerId"`
	Name         string    `json:"name"`
	State        string    `json:"state"`
	DeploymentID *string   `json:"deploymentId,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// InsertMetricSample appends one resource-usage sample for serverID.
func (s *Store) InsertMetricSample(ctx context.Context, serverID string, resources ResourceSnapshot) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO server_metric_samples (server_id, cpu_percent, mem_percent, disk_percent)
		VALUES ($1, $2, $3, $4)
	`, serverID, resources.CPUPercent, resources.MemPercent, resources.DiskPercent)
	return err
}

// ListMetricSamples returns serverID's samples recorded at or after since,
// oldest first (the natural order for a chart's x-axis).
func (s *Store) ListMetricSamples(ctx context.Context, serverID string, since time.Time) ([]MetricSample, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT recorded_at, cpu_percent, mem_percent, disk_percent
		FROM server_metric_samples
		WHERE server_id = $1 AND recorded_at >= $2
		ORDER BY recorded_at ASC
	`, serverID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	samples := []MetricSample{}
	for rows.Next() {
		var m MetricSample
		if err := rows.Scan(&m.RecordedAt, &m.CPUPercent, &m.MemPercent, &m.DiskPercent); err != nil {
			return nil, err
		}
		samples = append(samples, m)
	}
	return samples, rows.Err()
}

// PruneMetricSamplesOlderThan deletes samples recorded before cutoff,
// across all servers. Called periodically by a retention loop — see
// cmd/controlplane. No downsampling/rollup in this MVP slice: old samples
// are dropped outright, not aggregated into coarser history.
func (s *Store) PruneMetricSamplesOlderThan(ctx context.Context, cutoff time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM server_metric_samples WHERE recorded_at < $1`, cutoff)
	return err
}

// ReplaceContainers overwrites serverID's entire container inventory with
// containers, in one transaction. A full replace (rather than a diff-based
// upsert) is the simplest correct way to drop containers that stopped
// existing since the last heartbeat, at the scale this MVP targets.
func (s *Store) ReplaceContainers(ctx context.Context, serverID string, containers []ContainerState) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM server_containers WHERE server_id = $1`, serverID); err != nil {
		return err
	}

	for _, c := range containers {
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_containers (server_id, container_id, name, state, deployment_id)
			VALUES ($1, $2, $3, $4, $5)
		`, serverID, c.ContainerID, c.Name, c.State, c.DeploymentID); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// ListContainers returns serverID's current container inventory.
func (s *Store) ListContainers(ctx context.Context, serverID string) ([]ContainerState, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT container_id, name, state, deployment_id, updated_at
		FROM server_containers
		WHERE server_id = $1
		ORDER BY name ASC
	`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	containers := []ContainerState{}
	for rows.Next() {
		var c ContainerState
		if err := rows.Scan(&c.ContainerID, &c.Name, &c.State, &c.DeploymentID, &c.UpdatedAt); err != nil {
			return nil, err
		}
		containers = append(containers, c)
	}
	return containers, rows.Err()
}
