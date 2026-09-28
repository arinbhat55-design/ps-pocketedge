package insights

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

const (
	// evaluateInterval is how often every running container is checked
	// against the alert rules and for abnormal usage. A few heartbeats'
	// worth — rules are about sustained conditions, not instant ones.
	evaluateInterval = 1 * time.Minute
	// historyWindow is how much sample history each tick loads: enough for
	// the anomaly baseline and leak detection (see leakMinSpan) and for
	// any sane rule duration. Rules longer than this are capped to it.
	historyWindow = 3 * time.Hour
)

// Evaluator periodically evaluates container alert rules and built-in
// abnormal-usage detection across the fleet, opening a container_alerts
// row when a condition starts, refreshing it while it lasts, and resolving
// it once it clears. It's the container counterpart to the database
// monitoring alerts, but persistent: it runs whether or not anyone has the
// app open.
type Evaluator struct {
	log *slog.Logger
	st  *store.Store
	now func() time.Time
}

// NewEvaluator builds an Evaluator; call Run to start it.
func NewEvaluator(log *slog.Logger, st *store.Store) *Evaluator {
	return &Evaluator{log: log, st: st, now: time.Now}
}

// Run evaluates every evaluateInterval until ctx is cancelled.
func (e *Evaluator) Run(ctx context.Context) {
	ticker := time.NewTicker(evaluateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.Evaluate(ctx); err != nil {
				e.log.Error("container alert evaluation failed", "error", err)
			}
		}
	}
}

// Evaluate runs one pass.
func (e *Evaluator) Evaluate(ctx context.Context) error {
	now := e.now()
	rules, err := e.st.ListContainerAlertRules(ctx, true)
	if err != nil {
		return fmt.Errorf("list rules: %w", err)
	}
	containers, err := e.st.ListContainersFiltered(ctx, store.ContainerFilter{Status: "running"})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	samples, err := e.st.ListContainerMetricSamplesSince(ctx, now.Add(-historyWindow))
	if err != nil {
		return fmt.Errorf("list samples: %w", err)
	}
	open, err := e.st.ListContainerAlerts(ctx, store.ContainerAlertFilter{OpenOnly: true})
	if err != nil {
		return fmt.Errorf("list open alerts: %w", err)
	}

	firing := Conditions(rules, containers, samples, now)

	openByKey := make(map[string]store.ContainerAlert, len(open))
	for _, a := range open {
		openByKey[a.Key()] = a
	}
	for key, cond := range firing {
		if existing, ok := openByKey[key]; ok {
			if err := e.st.RefreshContainerAlert(ctx, existing.ID, cond.ContainerID, cond.Severity, cond.Value, cond.Message); err != nil {
				e.log.Error("failed to refresh container alert", "alert_id", existing.ID, "error", err)
			}
			continue
		}
		if err := e.st.OpenContainerAlert(ctx, cond); err != nil {
			e.log.Error("failed to open container alert", "container", cond.ContainerName, "metric", cond.Metric, "error", err)
			continue
		}
		e.log.Info("container alert opened", "server_id", cond.ServerID, "container", cond.ContainerName, "kind", cond.Kind, "metric", cond.Metric, "message", cond.Message)
	}
	// Anything open that didn't fire this tick has cleared — including
	// alerts whose rule was disabled, or whose container stopped or was
	// removed.
	for key, a := range openByKey {
		if _, ok := firing[key]; ok {
			continue
		}
		if err := e.st.ResolveContainerAlert(ctx, a.ID); err != nil {
			e.log.Error("failed to resolve container alert", "alert_id", a.ID, "error", err)
		}
	}
	return nil
}

// Conditions computes every currently-firing alert condition, keyed by
// ContainerAlert.Key: each enabled rule against each running container it
// matches, plus abnormal-usage detection for every running container.
// Split out from Evaluate so it's testable without a database.
func Conditions(rules []store.ContainerAlertRule, containers []store.FleetContainer, samples map[string][]store.ContainerResourceUsage, now time.Time) map[string]store.ContainerAlert {
	firing := map[string]store.ContainerAlert{}
	for _, c := range containers {
		history := samples[c.ServerID+"|"+c.ContainerID]
		if len(history) == 0 {
			continue
		}
		for _, rule := range rules {
			if !rule.Matches(c.ServerID, c.Name) {
				continue
			}
			duration := min(time.Duration(rule.DurationSeconds)*time.Second, historyWindow)
			res := EvaluateThreshold(history, rule.Metric, rule.Threshold, duration, now)
			if !res.Breaching {
				continue
			}
			threshold := rule.Threshold
			ruleID := rule.ID
			a := store.ContainerAlert{
				Kind:          "threshold",
				RuleID:        &ruleID,
				ServerID:      c.ServerID,
				ContainerID:   c.ContainerID,
				ContainerName: c.Name,
				Metric:        rule.Metric,
				Severity:      rule.Severity,
				Threshold:     &threshold,
				Value:         res.Value,
				Message:       thresholdMessage(rule, res.Value),
			}
			firing[a.Key()] = a
		}
		for _, an := range DetectAnomalies(history, now) {
			kind := an.Kind
			a := store.ContainerAlert{
				Kind:          "anomaly",
				AnomalyKind:   &kind,
				ServerID:      c.ServerID,
				ContainerID:   c.ContainerID,
				ContainerName: c.Name,
				Metric:        an.Metric,
				Severity:      an.Severity,
				Value:         an.Current,
				Message:       an.Message,
			}
			firing[a.Key()] = a
		}
	}
	return firing
}

func thresholdMessage(rule store.ContainerAlertRule, value float64) string {
	msg := fmt.Sprintf("%s is %s, above the %s threshold", MetricLabel(rule.Metric), FormatValue(rule.Metric, value), FormatValue(rule.Metric, rule.Threshold))
	if rule.DurationSeconds > 0 {
		msg += fmt.Sprintf(" for %s", humanDuration(time.Duration(rule.DurationSeconds)*time.Second))
	}
	return msg + fmt.Sprintf(" (rule %q).", rule.Name)
}
