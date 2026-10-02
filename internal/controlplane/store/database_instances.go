package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// DatabaseInstance mirrors a row in database_instances, joined with the
// live state of its deployment and server for display.
type DatabaseInstance struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Engine           string     `json:"engine"`
	Version          string     `json:"version"`
	DeploymentID     string     `json:"deploymentId"`
	ComposeFileID    *string    `json:"composeFileId,omitempty"`
	ServerID         string     `json:"serverId"`
	PrimaryService   string     `json:"primaryService"`
	ServerName       string     `json:"serverName"`
	ServerHostname   string     `json:"serverHostname"`
	DatabaseName     string     `json:"databaseName"`
	AdminUsername    string     `json:"adminUsername"`
	Port             int        `json:"port"`
	Access           string     `json:"access"`
	Profile          string     `json:"profile"`
	StorageGB        int        `json:"storageGb"`
	MemoryMB         int        `json:"memoryMb"`
	CPUs             float64    `json:"cpus"`
	HighAvailability bool       `json:"highAvailability"`
	AdminSecretID    *string    `json:"adminSecretId,omitempty"`
	BackupCron       *string    `json:"backupCron,omitempty"`
	BackupNextRunAt  *time.Time `json:"backupNextRunAt,omitempty"`
	BackupLastRunAt  *time.Time `json:"backupLastRunAt,omitempty"`
	BackupLastStatus *string    `json:"backupLastStatus,omitempty"`
	BackupConsistent bool       `json:"backupConsistent"`
	RetentionDays    int        `json:"retentionDays"`
	RetentionCount   int        `json:"retentionCount"`
	CreatedBy        *string    `json:"createdBy,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	// From the deployment.
	Phase        string `json:"phase"`
	HealthStatus string `json:"healthStatus"`
}

// NewDatabaseInstance is the input to InsertDatabaseInstance.
type NewDatabaseInstance struct {
	Name             string
	Engine           string
	Version          string
	DeploymentID     string
	ComposeFileID    string
	ServerID         string
	PrimaryService   string
	DatabaseName     string
	AdminUsername    string
	Port             int
	Access           string
	Profile          string
	StorageGB        int
	MemoryMB         int
	CPUs             float64
	HighAvailability bool
	AdminSecretID    string
	BackupPolicy
	CreatedBy string
}

// BackupPolicy is a database instance's editable backup settings.
// NextRunAt is computed by the caller from Cron.
type BackupPolicy struct {
	Cron           *string
	NextRunAt      *time.Time
	Consistent     bool
	RetentionDays  int
	RetentionCount int
}

const databaseInstanceSelect = `
	SELECT d.id, d.name, d.engine, d.version, d.deployment_id, d.compose_file_id, d.server_id, d.primary_service,
	       s.name, s.hostname, d.database_name, d.admin_username, d.port, d.access, d.profile,
	       d.storage_gb, d.memory_mb, d.cpus::float8, d.high_availability, d.admin_secret_id,
	       d.backup_cron, d.backup_next_run_at, d.backup_last_run_at, d.backup_last_status,
	       d.backup_consistent, d.retention_days, d.retention_count, d.created_by, d.created_at,
	       dep.phase, dep.health_status
	FROM database_instances d
	JOIN servers s ON s.id = d.server_id
	JOIN deployments dep ON dep.id = d.deployment_id
`

func scanDatabaseInstance(row rowScanner) (DatabaseInstance, error) {
	var d DatabaseInstance
	err := row.Scan(&d.ID, &d.Name, &d.Engine, &d.Version, &d.DeploymentID, &d.ComposeFileID, &d.ServerID, &d.PrimaryService,
		&d.ServerName, &d.ServerHostname, &d.DatabaseName, &d.AdminUsername, &d.Port, &d.Access, &d.Profile,
		&d.StorageGB, &d.MemoryMB, &d.CPUs, &d.HighAvailability, &d.AdminSecretID,
		&d.BackupCron, &d.BackupNextRunAt, &d.BackupLastRunAt, &d.BackupLastStatus,
		&d.BackupConsistent, &d.RetentionDays, &d.RetentionCount, &d.CreatedBy, &d.CreatedAt,
		&d.Phase, &d.HealthStatus)
	return d, err
}

// InsertDatabaseInstance records a database deployed from the catalog.
func (s *Store) InsertDatabaseInstance(ctx context.Context, n NewDatabaseInstance) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO database_instances (
			name, engine, version, deployment_id, compose_file_id, server_id, primary_service, database_name,
			admin_username, port, access, profile, storage_gb, memory_mb, cpus, high_availability,
			admin_secret_id, backup_cron, backup_next_run_at, backup_consistent, retention_days,
			retention_count, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
		RETURNING id
	`, n.Name, n.Engine, n.Version, n.DeploymentID, nullableUUID(n.ComposeFileID), n.ServerID, n.PrimaryService, n.DatabaseName,
		n.AdminUsername, n.Port, n.Access, n.Profile, n.StorageGB, n.MemoryMB, n.CPUs, n.HighAvailability,
		nullableUUID(n.AdminSecretID), n.Cron, n.NextRunAt, n.Consistent, n.RetentionDays,
		n.RetentionCount, nullableUUID(n.CreatedBy)).Scan(&id)
	return id, err
}

// GetDatabaseInstance returns one instance. Returns ErrNotFound if it
// doesn't exist.
func (s *Store) GetDatabaseInstance(ctx context.Context, id string) (*DatabaseInstance, error) {
	d, err := scanDatabaseInstance(s.pool.QueryRow(ctx, databaseInstanceSelect+` WHERE d.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetDatabaseInstanceByDeployment returns the instance backed by
// deploymentID. Returns ErrNotFound if the deployment isn't a database.
func (s *Store) GetDatabaseInstanceByDeployment(ctx context.Context, deploymentID string) (*DatabaseInstance, error) {
	d, err := scanDatabaseInstance(s.pool.QueryRow(ctx, databaseInstanceSelect+` WHERE d.deployment_id = $1`, deploymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ListDatabaseInstances returns every instance on a non-retired server,
// newest first.
func (s *Store) ListDatabaseInstances(ctx context.Context) ([]DatabaseInstance, error) {
	rows, err := s.pool.Query(ctx, databaseInstanceSelect+` WHERE s.removed_at IS NULL ORDER BY d.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DatabaseInstance{}
	for rows.Next() {
		d, err := scanDatabaseInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DatabaseNameTaken reports whether an instance already uses name. Names
// are unique fleet-wide: they name the instance's Compose file.
func (s *Store) DatabaseNameTaken(ctx context.Context, name string) (bool, error) {
	var taken bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM database_instances WHERE name = $1)`, name).Scan(&taken)
	return taken, err
}

// DatabasePortTaken returns the name of an instance on serverID already
// assigned port, if any — including one whose containers aren't running
// right now (awaiting approval, stopped), which a live port check would
// miss.
func (s *Store) DatabasePortTaken(ctx context.Context, serverID string, port int) (string, bool, error) {
	var name string
	err := s.pool.QueryRow(ctx, `SELECT name FROM database_instances WHERE server_id = $1 AND port = $2 LIMIT 1`, serverID, port).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return name, err == nil, err
}

// UpdateDatabaseBackupPolicy replaces an instance's backup policy.
func (s *Store) UpdateDatabaseBackupPolicy(ctx context.Context, id string, p BackupPolicy) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE database_instances
		SET backup_cron = $2, backup_next_run_at = $3, backup_consistent = $4, retention_days = $5, retention_count = $6
		WHERE id = $1
	`, id, p.Cron, p.NextRunAt, p.Consistent, p.RetentionDays, p.RetentionCount)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// UpdateDatabaseConfiguration records the desired image and resource plan
// after its Compose definition has been versioned for a redeploy.
func (s *Store) UpdateDatabaseConfiguration(ctx context.Context, id, version string, memoryMB int, cpus float64, storageGB int) error {
	tag, err := s.pool.Exec(ctx, `UPDATE database_instances SET version=$2,memory_mb=$3,cpus=$4,storage_gb=$5 WHERE id=$1`, id, version, memoryMB, cpus, storageGB)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteDatabaseInstance removes the marketplace record and its vaulted
// credentials after the underlying deployment has been removed. Deployment
// history and backups are retained separately.
func (s *Store) DeleteDatabaseInstance(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM database_instances WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DueDatabaseBackups returns instances whose scheduled backup is due.
func (s *Store) DueDatabaseBackups(ctx context.Context, now time.Time) ([]DatabaseInstance, error) {
	rows, err := s.pool.Query(ctx, databaseInstanceSelect+`
		WHERE d.backup_cron IS NOT NULL AND d.backup_next_run_at <= $1 AND s.removed_at IS NULL
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DatabaseInstance{}
	for rows.Next() {
		d, err := scanDatabaseInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RecordDatabaseBackupRun stores a scheduled backup's outcome and when the
// next one is due.
func (s *Store) RecordDatabaseBackupRun(ctx context.Context, id, status string, nextRunAt *time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE database_instances
		SET backup_last_run_at = now(), backup_last_status = $2, backup_next_run_at = $3
		WHERE id = $1
	`, id, status, nextRunAt)
	return err
}

// ListDatabaseInstancesWithRetention returns instances that have any
// retention limit set.
func (s *Store) ListDatabaseInstancesWithRetention(ctx context.Context) ([]DatabaseInstance, error) {
	rows, err := s.pool.Query(ctx, databaseInstanceSelect+` WHERE d.retention_days > 0 OR d.retention_count > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DatabaseInstance{}
	for rows.Next() {
		d, err := scanDatabaseInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
