package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ContainerAlertRule mirrors a row in container_alert_rules: "alert when
// Metric stays above Threshold for DurationSeconds". ServerID and
// ContainerName narrow its scope; nil means "every server" / "every
// container". Containers are matched by name rather than ID so a rule
// survives the container being recreated (which changes its ID).
type ContainerAlertRule struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	ServerID        *string   `json:"serverId,omitempty"`
	ContainerName   *string   `json:"containerName,omitempty"`
	Metric          string    `json:"metric"` // "cpu", "memory", or "pids"
	Threshold       float64   `json:"threshold"`
	DurationSeconds int       `json:"durationSeconds"`
	Severity        string    `json:"severity"` // "warning" or "critical"
	Enabled         bool      `json:"enabled"`
	CreatedBy       *string   `json:"createdBy,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Matches reports whether the rule applies to a container named
// containerName on serverID.
func (r ContainerAlertRule) Matches(serverID, containerName string) bool {
	if r.ServerID != nil && *r.ServerID != serverID {
		return false
	}
	if r.ContainerName != nil && *r.ContainerName != containerName {
		return false
	}
	return true
}

const containerAlertRuleColumns = `id, name, server_id, container_name, metric, threshold, duration_seconds, severity, enabled, created_by, created_at`

func scanContainerAlertRule(row pgx.Row) (*ContainerAlertRule, error) {
	var r ContainerAlertRule
	if err := row.Scan(&r.ID, &r.Name, &r.ServerID, &r.ContainerName, &r.Metric, &r.Threshold, &r.DurationSeconds, &r.Severity, &r.Enabled, &r.CreatedBy, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// CreateContainerAlertRule inserts a rule and returns its ID. Callers
// validate metric/threshold/severity first.
func (s *Store) CreateContainerAlertRule(ctx context.Context, r ContainerAlertRule) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO container_alert_rules (name, server_id, container_name, metric, threshold, duration_seconds, severity, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, r.Name, r.ServerID, r.ContainerName, r.Metric, r.Threshold, r.DurationSeconds, r.Severity, r.Enabled, r.CreatedBy).Scan(&id)
	return id, err
}

// GetContainerAlertRule looks up one rule. Returns ErrNotFound if missing.
func (s *Store) GetContainerAlertRule(ctx context.Context, id string) (*ContainerAlertRule, error) {
	r, err := scanContainerAlertRule(s.pool.QueryRow(ctx, `SELECT `+containerAlertRuleColumns+` FROM container_alert_rules WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// ListContainerAlertRules returns every rule, newest first; enabledOnly
// limits it to the ones the evaluator should check.
func (s *Store) ListContainerAlertRules(ctx context.Context, enabledOnly bool) ([]ContainerAlertRule, error) {
	query := `SELECT ` + containerAlertRuleColumns + ` FROM container_alert_rules`
	if enabledOnly {
		query += ` WHERE enabled`
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := []ContainerAlertRule{}
	for rows.Next() {
		r, err := scanContainerAlertRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, *r)
	}
	return rules, rows.Err()
}

// UpdateContainerAlertRule overwrites a rule's editable fields. Returns
// ErrNotFound if it doesn't exist.
func (s *Store) UpdateContainerAlertRule(ctx context.Context, r ContainerAlertRule) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE container_alert_rules
		SET name = $2, server_id = $3, container_name = $4, metric = $5, threshold = $6,
		    duration_seconds = $7, severity = $8, enabled = $9
		WHERE id = $1
	`, r.ID, r.Name, r.ServerID, r.ContainerName, r.Metric, r.Threshold, r.DurationSeconds, r.Severity, r.Enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteContainerAlertRule removes a rule, resolving any of its alerts
// still open (their rule_id becomes NULL via ON DELETE SET NULL, keeping
// the history). Returns ErrNotFound if it doesn't exist.
func (s *Store) DeleteContainerAlertRule(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `UPDATE container_alerts SET resolved_at = now() WHERE rule_id = $1 AND resolved_at IS NULL`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM container_alert_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// ContainerAlert mirrors a row in container_alerts: one firing (open) or
// past (resolved) alert on one container. Kind is "threshold" (raised by a
// ContainerAlertRule, RuleID set) or "anomaly" (raised by built-in
// abnormal-usage detection, RuleID nil, AnomalyKind set).
type ContainerAlert struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	RuleID         *string    `json:"ruleId,omitempty"`
	RuleName       *string    `json:"ruleName,omitempty"`
	AnomalyKind    *string    `json:"anomalyKind,omitempty"`
	ServerID       string     `json:"serverId"`
	ServerName     string     `json:"serverName"`
	ContainerID    string     `json:"containerId"`
	ContainerName  string     `json:"containerName"`
	Metric         string     `json:"metric"`
	Severity       string     `json:"severity"`
	Threshold      *float64   `json:"threshold,omitempty"`
	Value          float64    `json:"value"`
	Message        string     `json:"message"`
	StartedAt      time.Time  `json:"startedAt"`
	LastSeenAt     time.Time  `json:"lastSeenAt"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
	AcknowledgedBy *string    `json:"acknowledgedBy,omitempty"`
}

// Key identifies "the same condition on the same container" across
// evaluator ticks, so a still-firing condition updates its open alert
// instead of opening a new one each minute.
func (a ContainerAlert) Key() string {
	rule, anomaly := "", ""
	if a.RuleID != nil {
		rule = *a.RuleID
	}
	if a.AnomalyKind != nil {
		anomaly = *a.AnomalyKind
	}
	return strings.Join([]string{a.Kind, rule, anomaly, a.Metric, a.ServerID, a.ContainerName}, "|")
}

const containerAlertSelect = `
	SELECT a.id, a.kind, a.rule_id, r.name, a.anomaly_kind, a.server_id, COALESCE(srv.name, ''),
	       a.container_id, a.container_name, a.metric, a.severity, a.threshold, a.value, a.message,
	       a.started_at, a.last_seen_at, a.resolved_at, a.acknowledged_at, a.acknowledged_by
	FROM container_alerts a
	LEFT JOIN container_alert_rules r ON r.id = a.rule_id
	LEFT JOIN servers srv ON srv.id = a.server_id
`

func scanContainerAlert(row pgx.Row) (*ContainerAlert, error) {
	var a ContainerAlert
	if err := row.Scan(&a.ID, &a.Kind, &a.RuleID, &a.RuleName, &a.AnomalyKind, &a.ServerID, &a.ServerName,
		&a.ContainerID, &a.ContainerName, &a.Metric, &a.Severity, &a.Threshold, &a.Value, &a.Message,
		&a.StartedAt, &a.LastSeenAt, &a.ResolvedAt, &a.AcknowledgedAt, &a.AcknowledgedBy); err != nil {
		return nil, err
	}
	return &a, nil
}

// ContainerAlertFilter narrows ListContainerAlerts; zero fields are
// ignored. OpenOnly limits it to unresolved alerts.
type ContainerAlertFilter struct {
	OpenOnly    bool
	ServerID    string
	ContainerID string
	Limit       int
}

// ListContainerAlerts returns alerts matching filter, open ones first,
// then most recently started.
func (s *Store) ListContainerAlerts(ctx context.Context, f ContainerAlertFilter) ([]ContainerAlert, error) {
	query := containerAlertSelect
	var conditions []string
	var args []any
	add := func(cond string, arg any) {
		args = append(args, arg)
		conditions = append(conditions, fmt.Sprintf(cond, len(args)))
	}
	if f.OpenOnly {
		conditions = append(conditions, "a.resolved_at IS NULL")
	}
	if f.ServerID != "" {
		add("a.server_id = $%d", f.ServerID)
	}
	if f.ContainerID != "" {
		add("a.container_id = $%d", f.ContainerID)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY (a.resolved_at IS NULL) DESC, a.started_at DESC"
	if f.Limit > 0 {
		args = append(args, f.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	alerts := []ContainerAlert{}
	for rows.Next() {
		a, err := scanContainerAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, *a)
	}
	return alerts, rows.Err()
}

// OpenContainerAlert inserts a new firing alert.
func (s *Store) OpenContainerAlert(ctx context.Context, a ContainerAlert) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO container_alerts
			(kind, rule_id, anomaly_kind, server_id, container_id, container_name, metric, severity, threshold, value, message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, a.Kind, a.RuleID, a.AnomalyKind, a.ServerID, a.ContainerID, a.ContainerName, a.Metric, a.Severity, a.Threshold, a.Value, a.Message)
	return err
}

// RefreshContainerAlert records that an open alert is still firing, with
// its latest value/message/severity (and container ID, in case the
// container was recreated under the same name).
func (s *Store) RefreshContainerAlert(ctx context.Context, id, containerID, severity string, value float64, message string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE container_alerts
		SET last_seen_at = now(), container_id = $2, severity = $3, value = $4, message = $5
		WHERE id = $1
	`, id, containerID, severity, value, message)
	return err
}

// ResolveContainerAlert closes an open alert.
func (s *Store) ResolveContainerAlert(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE container_alerts SET resolved_at = now() WHERE id = $1 AND resolved_at IS NULL`, id)
	return err
}

// AcknowledgeContainerAlert marks an alert as seen by userID. Returns
// ErrNotFound if it doesn't exist.
func (s *Store) AcknowledgeContainerAlert(ctx context.Context, id, userID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE container_alerts SET acknowledged_at = now(), acknowledged_by = $2
		WHERE id = $1 AND acknowledged_at IS NULL
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := scanContainerAlert(s.pool.QueryRow(ctx, containerAlertSelect+` WHERE a.id = $1`, id)); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
	}
	return nil
}

// PruneResolvedContainerAlertsOlderThan deletes alerts resolved before
// cutoff, called by the same retention loop as the metric samples.
func (s *Store) PruneResolvedContainerAlertsOlderThan(ctx context.Context, cutoff time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM container_alerts WHERE resolved_at IS NOT NULL AND resolved_at < $1`, cutoff)
	return err
}

// ListContainerMetricSamplesSince returns every container's samples
// recorded at or after since, fleet-wide, grouped by "serverID|containerID"
// and oldest first within each group — one query for the alert evaluator
// instead of one per container.
func (s *Store) ListContainerMetricSamplesSince(ctx context.Context, since time.Time) (map[string][]ContainerResourceUsage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT server_id, container_id, recorded_at, cpu_percent, mem_usage_bytes, mem_limit_bytes, mem_percent, net_rx_bytes, net_tx_bytes, block_read_bytes, block_write_bytes, pids
		FROM container_metric_samples
		WHERE recorded_at >= $1
		ORDER BY server_id, container_id, recorded_at ASC
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]ContainerResourceUsage{}
	for rows.Next() {
		var serverID string
		var u ContainerResourceUsage
		if err := rows.Scan(&serverID, &u.ContainerID, &u.RecordedAt, &u.CPUPercent, &u.MemUsageBytes, &u.MemLimitBytes, &u.MemPercent, &u.NetRxBytes, &u.NetTxBytes, &u.BlockReadBytes, &u.BlockWriteBytes, &u.PIDs); err != nil {
			return nil, err
		}
		key := serverID + "|" + u.ContainerID
		out[key] = append(out[key], u)
	}
	return out, rows.Err()
}
