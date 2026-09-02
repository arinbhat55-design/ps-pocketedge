package store

import (
	"context"
	"encoding/json"
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

// ContainerResourceUsage mirrors a row in container_metric_samples — one
// point in a single container's resource-usage history, the per-container
// counterpart to MetricSample above.
type ContainerResourceUsage struct {
	ContainerID     string    `json:"containerId"`
	RecordedAt      time.Time `json:"recordedAt"`
	CPUPercent      float64   `json:"cpuPercent"`
	MemUsageBytes   int64     `json:"memUsageBytes"`
	MemLimitBytes   int64     `json:"memLimitBytes"`
	MemPercent      float64   `json:"memPercent"`
	NetRxBytes      int64     `json:"netRxBytes"`
	NetTxBytes      int64     `json:"netTxBytes"`
	BlockReadBytes  int64     `json:"blockReadBytes"`
	BlockWriteBytes int64     `json:"blockWriteBytes"`
	PIDs            int64     `json:"pids"`
}

// ContainerPort is one published or exposed port on a container.
type ContainerPort struct {
	IP          string `json:"ip,omitempty"`
	PrivatePort uint16 `json:"privatePort"`
	PublicPort  uint16 `json:"publicPort,omitempty"`
	Type        string `json:"type"`
}

// ContainerNetwork is one Docker network a container is attached to.
type ContainerNetwork struct {
	Name      string `json:"name"`
	IPAddress string `json:"ipAddress,omitempty"`
}

// ContainerMount is one volume or bind mount attached to a container.
type ContainerMount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadWrite   bool   `json:"readWrite"`
}

// ContainerState mirrors a row in server_containers — a server's current
// container inventory, replaced in full on every heartbeat (see
// ReplaceContainers). Image/ImageID/CreatedAt/Status/Ports/Networks/Mounts
// come from the agent's cheap ContainerList call, ridden on every
// heartbeat that refreshes containers — no extra Docker API round-trip.
type ContainerState struct {
	ContainerID  string             `json:"containerId"`
	Name         string             `json:"name"`
	State        string             `json:"state"`
	DeploymentID *string            `json:"deploymentId,omitempty"`
	Image        string             `json:"image,omitempty"`
	ImageID      string             `json:"imageId,omitempty"`
	CreatedAt    *time.Time         `json:"createdAt,omitempty"`
	Status       string             `json:"status,omitempty"`
	Ports        []ContainerPort    `json:"ports,omitempty"`
	Networks     []ContainerNetwork `json:"networks,omitempty"`
	Mounts       []ContainerMount   `json:"mounts,omitempty"`
	UpdatedAt    time.Time          `json:"updatedAt"`
}

// marshalContainerSlice marshals a ports/networks/mounts slice for a JSONB
// column, returning nil (SQL NULL) for an empty slice rather than the
// literal "[]" — a container with none of these just stores nothing.
func marshalContainerSlice[T any](v []T) ([]byte, error) {
	if len(v) == 0 {
		return nil, nil
	}
	return json.Marshal(v)
}

// unmarshalContainerSlice decodes a nullable JSONB column into dst, leaving
// dst as nil when raw is NULL/empty.
func unmarshalContainerSlice[T any](raw []byte, dst *[]T) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// InsertMetricSample appends one resource-usage sample for serverID,
// recorded at recordedAt (not defaulted to now()) so that samples replayed
// from the agent's offline buffer land at the time they were actually
// taken, not bunched at reconnect time.
func (s *Store) InsertMetricSample(ctx context.Context, serverID string, recordedAt time.Time, resources ResourceSnapshot) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO server_metric_samples (server_id, recorded_at, cpu_percent, mem_percent, disk_percent)
		VALUES ($1, $2, $3, $4, $5)
	`, serverID, recordedAt, resources.CPUPercent, resources.MemPercent, resources.DiskPercent)
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

// InsertContainerMetricSample appends one resource-usage sample for one
// container on serverID — the per-container counterpart to
// InsertMetricSample above, recorded at the same heartbeat-carried time for
// the same offline-buffer-replay reason.
func (s *Store) InsertContainerMetricSample(ctx context.Context, serverID string, recordedAt time.Time, usage ContainerResourceUsage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO container_metric_samples
			(server_id, container_id, recorded_at, cpu_percent, mem_usage_bytes, mem_limit_bytes, mem_percent, net_rx_bytes, net_tx_bytes, block_read_bytes, block_write_bytes, pids)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, serverID, usage.ContainerID, recordedAt, usage.CPUPercent, usage.MemUsageBytes, usage.MemLimitBytes, usage.MemPercent, usage.NetRxBytes, usage.NetTxBytes, usage.BlockReadBytes, usage.BlockWriteBytes, usage.PIDs)
	return err
}

// ListContainerMetricSamples returns one container's samples recorded at or
// after since, oldest first — the per-container counterpart to
// ListMetricSamples above.
func (s *Store) ListContainerMetricSamples(ctx context.Context, serverID, containerID string, since time.Time) ([]ContainerResourceUsage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT container_id, recorded_at, cpu_percent, mem_usage_bytes, mem_limit_bytes, mem_percent, net_rx_bytes, net_tx_bytes, block_read_bytes, block_write_bytes, pids
		FROM container_metric_samples
		WHERE server_id = $1 AND container_id = $2 AND recorded_at >= $3
		ORDER BY recorded_at ASC
	`, serverID, containerID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	samples := []ContainerResourceUsage{}
	for rows.Next() {
		var u ContainerResourceUsage
		if err := rows.Scan(&u.ContainerID, &u.RecordedAt, &u.CPUPercent, &u.MemUsageBytes, &u.MemLimitBytes, &u.MemPercent, &u.NetRxBytes, &u.NetTxBytes, &u.BlockReadBytes, &u.BlockWriteBytes, &u.PIDs); err != nil {
			return nil, err
		}
		samples = append(samples, u)
	}
	return samples, rows.Err()
}

// PruneContainerMetricSamplesOlderThan deletes container samples recorded
// before cutoff, across all servers/containers — the per-container
// counterpart to PruneMetricSamplesOlderThan above, called by the same
// retention loop (see cmd/controlplane).
func (s *Store) PruneContainerMetricSamplesOlderThan(ctx context.Context, cutoff time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM container_metric_samples WHERE recorded_at < $1`, cutoff)
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
		ports, err := marshalContainerSlice(c.Ports)
		if err != nil {
			return err
		}
		networks, err := marshalContainerSlice(c.Networks)
		if err != nil {
			return err
		}
		mounts, err := marshalContainerSlice(c.Mounts)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_containers (server_id, container_id, name, state, deployment_id, image, image_id, created_at, status, ports, networks, mounts)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`, serverID, c.ContainerID, c.Name, c.State, c.DeploymentID, c.Image, c.ImageID, c.CreatedAt, c.Status, ports, networks, mounts); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// ListContainers returns serverID's current container inventory.
func (s *Store) ListContainers(ctx context.Context, serverID string) ([]ContainerState, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT container_id, name, state, deployment_id, image, image_id, created_at, status, ports, networks, mounts, updated_at
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
		var ports, networks, mounts []byte
		if err := rows.Scan(&c.ContainerID, &c.Name, &c.State, &c.DeploymentID, &c.Image, &c.ImageID, &c.CreatedAt, &c.Status, &ports, &networks, &mounts, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if err := unmarshalContainerSlice(ports, &c.Ports); err != nil {
			return nil, err
		}
		if err := unmarshalContainerSlice(networks, &c.Networks); err != nil {
			return nil, err
		}
		if err := unmarshalContainerSlice(mounts, &c.Mounts); err != nil {
			return nil, err
		}
		containers = append(containers, c)
	}
	return containers, rows.Err()
}
