package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FleetContainer is one row of the fleet-wide inventory: a
// server_containers row joined with its server, and — where the container
// is deployment-managed — its deployment's stack ("application"), owning
// user, and grouping metadata (environment/tags). Containers with no
// deployment_id (unmanaged, or the deployment_id label was absent) carry
// nil/empty for every deployment-derived field.
type FleetContainer struct {
	ServerID     string             `json:"serverId"`
	ServerName   string             `json:"serverName"`
	ContainerID  string             `json:"containerId"`
	Name         string             `json:"name"`
	State        string             `json:"state"`
	Status       string             `json:"status,omitempty"`
	Image        string             `json:"image,omitempty"`
	ImageID      string             `json:"imageId,omitempty"`
	CreatedAt    *time.Time         `json:"createdAt,omitempty"`
	Ports        []ContainerPort    `json:"ports,omitempty"`
	Networks     []ContainerNetwork `json:"networks,omitempty"`
	Mounts       []ContainerMount   `json:"mounts,omitempty"`
	DeploymentID *string            `json:"deploymentId,omitempty"`
	StackID      *string            `json:"stackId,omitempty"`
	StackName    *string            `json:"stackName,omitempty"`
	OwnerID      *string            `json:"ownerId,omitempty"`
	OwnerEmail   *string            `json:"ownerEmail,omitempty"`
	Environment  *string            `json:"environment,omitempty"`
	Tags         []string           `json:"tags,omitempty"`
	UpdatedAt    time.Time          `json:"updatedAt"`
}

// ContainerFilter narrows ListContainersFiltered's result. Every non-zero
// field is ANDed together; Tags matches containers whose deployment has at
// least one of the listed tags (array overlap), not all of them.
type ContainerFilter struct {
	Name         string
	Image        string
	ServerID     string
	Status       string
	OwnerID      string
	Environment  string
	Tags         []string
	DeploymentID string
}

// ListContainersFiltered returns the fleet-wide container inventory,
// joined with server/stack/owner, filtered by whichever ContainerFilter
// fields are set, ordered by server name then container name. A small
// hand-rolled WHERE builder — this codebase has no query builder/ORM and
// one isn't warranted for six optional equality/substring/overlap filters.
func (s *Store) ListContainersFiltered(ctx context.Context, f ContainerFilter) ([]FleetContainer, error) {
	query := `
		SELECT sc.server_id, srv.name, sc.container_id, sc.name, sc.state, sc.status,
		       sc.image, sc.image_id, sc.created_at, sc.ports, sc.networks, sc.mounts,
		       sc.deployment_id, COALESCE(d.stack_id, d.compose_file_id), COALESCE(st.name, cf.name),
		       d.created_by, u.email, d.deploy_environment, d.tags, sc.updated_at
		FROM server_containers sc
		JOIN servers srv ON srv.id = sc.server_id
		LEFT JOIN deployments d ON d.id = sc.deployment_id
		LEFT JOIN stacks st ON st.id = d.stack_id
		LEFT JOIN compose_files cf ON cf.id = d.compose_file_id
		LEFT JOIN users u ON u.id = d.created_by
	`

	var conditions []string
	var args []any
	add := func(cond string, arg any) {
		args = append(args, arg)
		conditions = append(conditions, fmt.Sprintf(cond, len(args)))
	}
	if f.Name != "" {
		add("sc.name ILIKE '%%' || $%d || '%%'", f.Name)
	}
	if f.Image != "" {
		add("sc.image ILIKE '%%' || $%d || '%%'", f.Image)
	}
	if f.ServerID != "" {
		add("sc.server_id = $%d", f.ServerID)
	}
	if f.Status != "" {
		add("sc.state = $%d", f.Status)
	}
	if f.OwnerID != "" {
		add("d.created_by = $%d", f.OwnerID)
	}
	if f.Environment != "" {
		add("d.deploy_environment = $%d", f.Environment)
	}
	if len(f.Tags) > 0 {
		add("d.tags && $%d", f.Tags)
	}
	if f.DeploymentID != "" {
		add("sc.deployment_id = $%d", f.DeploymentID)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY srv.name ASC, sc.name ASC"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	containers := []FleetContainer{}
	for rows.Next() {
		var c FleetContainer
		var ports, networks, mounts []byte
		if err := rows.Scan(
			&c.ServerID, &c.ServerName, &c.ContainerID, &c.Name, &c.State, &c.Status,
			&c.Image, &c.ImageID, &c.CreatedAt, &ports, &networks, &mounts,
			&c.DeploymentID, &c.StackID, &c.StackName, &c.OwnerID, &c.OwnerEmail,
			&c.Environment, &c.Tags, &c.UpdatedAt,
		); err != nil {
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

// FindPortConflict reports the container currently bound to hostPort/
// protocol on serverID, using the same cached sc.ports JSONB
// ListContainersFiltered already reads (refreshed on every heartbeat — see
// migrations/0006_container_management.up.sql) rather than a live agent
// round trip. Filtered in Go rather than via a JSONB containment query,
// consistent with this codebase's general preference for explicit Go logic
// over query-builder cleverness (see ListContainersFiltered's WHERE
// builder). found is false if no container currently claims that port.
func (s *Store) FindPortConflict(ctx context.Context, serverID string, hostPort uint16, protocol string) (containerID string, found bool, err error) {
	containers, err := s.ListContainersFiltered(ctx, ContainerFilter{ServerID: serverID})
	if err != nil {
		return "", false, err
	}
	for _, c := range containers {
		for _, p := range c.Ports {
			if p.PublicPort == hostPort && p.Type == protocol {
				return c.ContainerID, true, nil
			}
		}
	}
	return "", false, nil
}
