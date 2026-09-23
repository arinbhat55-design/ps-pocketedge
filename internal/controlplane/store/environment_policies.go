package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// MaintenanceWindow is one recurring weekly window during which changes to
// an environment may run. Days are weekdays in UTC (Sunday = 0); Start/End
// are "HH:MM" in UTC. A window whose End is at or before its Start wraps
// past midnight into the following day.
type MaintenanceWindow struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// EnvironmentPolicy is one environment's governance rules.
type EnvironmentPolicy struct {
	Environment              string              `json:"environment"`
	RequireApproval          bool                `json:"requireApproval"`
	AllowSelfApproval        bool                `json:"allowSelfApproval"`
	RequireChangeRequest     bool                `json:"requireChangeRequest"`
	RequireRollbackPlan      bool                `json:"requireRollbackPlan"`
	EnforceMaintenanceWindow bool                `json:"enforceMaintenanceWindow"`
	MaintenanceWindows       []MaintenanceWindow `json:"maintenanceWindows"`
	UpdatedBy                *string             `json:"updatedBy,omitempty"`
	UpdatedAt                time.Time           `json:"updatedAt"`
}

func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q (want HH:MM)", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Validate reports the first malformed window, if any.
func (w MaintenanceWindow) Validate() error {
	if len(w.Days) == 0 {
		return errors.New("maintenance window needs at least one day")
	}
	for _, d := range w.Days {
		if d < 0 || d > 6 {
			return fmt.Errorf("invalid weekday %d (want 0-6, Sunday = 0)", d)
		}
	}
	if _, err := parseClock(w.Start); err != nil {
		return err
	}
	if _, err := parseClock(w.End); err != nil {
		return err
	}
	return nil
}

// occurrences returns this window's concrete [start, end) intervals that
// begin on the days from `from`-1 day through `from`+7 days — enough to
// answer both "is now inside a window" (including one that started
// yesterday and wraps past midnight) and "when does the next one start".
func (w MaintenanceWindow) occurrences(from time.Time) [][2]time.Time {
	start, err1 := parseClock(w.Start)
	end, err2 := parseClock(w.End)
	if err1 != nil || err2 != nil {
		return nil
	}
	duration := end - start
	if duration <= 0 {
		duration += 24 * 60
	}
	days := map[int]bool{}
	for _, d := range w.Days {
		days[d] = true
	}
	from = from.UTC()
	midnight := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	var out [][2]time.Time
	for offset := -1; offset <= 7; offset++ {
		day := midnight.AddDate(0, 0, offset)
		if !days[int(day.Weekday())] {
			continue
		}
		s := day.Add(time.Duration(start) * time.Minute)
		out = append(out, [2]time.Time{s, s.Add(time.Duration(duration) * time.Minute)})
	}
	return out
}

// InMaintenanceWindow reports whether now falls inside any of windows.
func InMaintenanceWindow(windows []MaintenanceWindow, now time.Time) bool {
	for _, w := range windows {
		for _, o := range w.occurrences(now) {
			if !now.Before(o[0]) && now.Before(o[1]) {
				return true
			}
		}
	}
	return false
}

// NextMaintenanceWindow returns the start of the next window strictly after
// now, or false if windows is empty/invalid.
func NextMaintenanceWindow(windows []MaintenanceWindow, now time.Time) (time.Time, bool) {
	var starts []time.Time
	for _, w := range windows {
		for _, o := range w.occurrences(now) {
			if o[0].After(now) {
				starts = append(starts, o[0])
			}
		}
	}
	if len(starts) == 0 {
		return time.Time{}, false
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	return starts[0], true
}

// ChangeAllowedNow reports whether the policy's maintenance window (if
// enforced) permits a change at now.
func (p *EnvironmentPolicy) ChangeAllowedNow(now time.Time) bool {
	if p == nil || !p.EnforceMaintenanceWindow {
		return true
	}
	return InMaintenanceWindow(p.MaintenanceWindows, now)
}

// ListEnvironmentPolicies returns every environment's policy, in the
// development/test/staging/production order.
func (s *Store) ListEnvironmentPolicies(ctx context.Context) ([]EnvironmentPolicy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT environment, require_approval, allow_self_approval, require_change_request, require_rollback_plan,
			enforce_maintenance_window, maintenance_windows, updated_by, updated_at
		FROM environment_policies
		ORDER BY array_position(ARRAY['development', 'test', 'staging', 'production'], environment)
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnvironmentPolicy{}
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func scanPolicy(row pgx.Row) (*EnvironmentPolicy, error) {
	var p EnvironmentPolicy
	var windows []byte
	if err := row.Scan(&p.Environment, &p.RequireApproval, &p.AllowSelfApproval, &p.RequireChangeRequest, &p.RequireRollbackPlan,
		&p.EnforceMaintenanceWindow, &windows, &p.UpdatedBy, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(windows, &p.MaintenanceWindows); err != nil {
		return nil, err
	}
	if p.MaintenanceWindows == nil {
		p.MaintenanceWindows = []MaintenanceWindow{}
	}
	return &p, nil
}

// GetEnvironmentPolicy returns environment's policy, or nil (no rules) for
// an empty/unknown environment — a deployment without an environment isn't
// governed.
func (s *Store) GetEnvironmentPolicy(ctx context.Context, environment string) (*EnvironmentPolicy, error) {
	if environment == "" {
		return nil, nil
	}
	p, err := scanPolicy(s.pool.QueryRow(ctx, `
		SELECT environment, require_approval, allow_self_approval, require_change_request, require_rollback_plan,
			enforce_maintenance_window, maintenance_windows, updated_by, updated_at
		FROM environment_policies WHERE environment = $1
	`, environment))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// UpsertEnvironmentPolicy saves p (keyed by p.Environment).
func (s *Store) UpsertEnvironmentPolicy(ctx context.Context, p EnvironmentPolicy, updatedBy string) error {
	if p.MaintenanceWindows == nil {
		p.MaintenanceWindows = []MaintenanceWindow{}
	}
	windows, err := json.Marshal(p.MaintenanceWindows)
	if err != nil {
		return err
	}
	var by any
	if updatedBy != "" {
		by = updatedBy
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO environment_policies (environment, require_approval, allow_self_approval, require_change_request, require_rollback_plan,
			enforce_maintenance_window, maintenance_windows, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (environment) DO UPDATE SET
			require_approval = EXCLUDED.require_approval, allow_self_approval = EXCLUDED.allow_self_approval,
			require_change_request = EXCLUDED.require_change_request, require_rollback_plan = EXCLUDED.require_rollback_plan,
			enforce_maintenance_window = EXCLUDED.enforce_maintenance_window, maintenance_windows = EXCLUDED.maintenance_windows,
			updated_by = EXCLUDED.updated_by, updated_at = now()
	`, p.Environment, p.RequireApproval, p.AllowSelfApproval, p.RequireChangeRequest, p.RequireRollbackPlan,
		p.EnforceMaintenanceWindow, windows, by)
	return err
}
