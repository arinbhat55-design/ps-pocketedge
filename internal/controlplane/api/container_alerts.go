package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/insights"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// defaultInsightsWindow is how much history GET .../insights analyzes when
// the client doesn't pass ?since=. Long enough for a meaningful baseline
// and for recommendations to see more than one burst; metric retention
// (24h, see cmd/controlplane) caps it anyway.
const defaultInsightsWindow = 6 * time.Hour

// maxAlertRuleDuration caps a rule's sustain duration at the evaluator's
// history window — a longer one could never be satisfied.
const maxAlertRuleDuration = 3 * time.Hour

// handleContainerInsights serves one container's abnormal-usage findings,
// resource-allocation recommendations (sized against the limits the
// client passes — nanoCpus/memoryLimitBytes/memoryReservationBytes/
// pidsLimit, as ContainerInspect reports them, 0 or absent = unlimited),
// the usage stats they were based on, and its open alerts.
func handleContainerInsights(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type response struct {
		Stats           *insights.UsageStats      `json:"stats,omitempty"`
		Anomalies       []insights.Anomaly        `json:"anomalies"`
		Recommendations []insights.Recommendation `json:"recommendations"`
		CurrentLimits   insights.Limits           `json:"currentLimits"`
		SuggestedLimits insights.Limits           `json:"suggestedLimits"`
		OpenAlerts      []store.ContainerAlert    `json:"openAlerts"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")
		q := r.URL.Query()

		window := defaultInsightsWindow
		if raw := q.Get("since"); raw != "" {
			parsed, err := time.ParseDuration(raw)
			if err != nil || parsed <= 0 {
				http.Error(w, "invalid since duration", http.StatusBadRequest)
				return
			}
			window = parsed
		}
		var limits insights.Limits
		for name, dst := range map[string]*int64{
			"nanoCpus":               &limits.NanoCPUs,
			"memoryLimitBytes":       &limits.MemoryLimitBytes,
			"memoryReservationBytes": &limits.MemoryReservationBytes,
			"pidsLimit":              &limits.PidsLimit,
		} {
			raw := q.Get(name)
			if raw == "" {
				continue
			}
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				http.Error(w, "invalid "+name, http.StatusBadRequest)
				return
			}
			// Docker reports an unlimited pids limit as -1 or 0.
			*dst = max(v, 0)
		}

		now := time.Now()
		samples, err := st.ListContainerMetricSamples(r.Context(), serverID, containerID, now.Add(-window))
		if err != nil {
			log.Error("failed to list container metric samples", "server_id", serverID, "container_id", containerID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		openAlerts, err := st.ListContainerAlerts(r.Context(), store.ContainerAlertFilter{OpenOnly: true, ServerID: serverID, ContainerID: containerID})
		if err != nil {
			log.Error("failed to list container alerts", "server_id", serverID, "container_id", containerID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		resp := response{
			Anomalies:       insights.DetectAnomalies(samples, now),
			Recommendations: insights.Recommend(samples, limits),
			CurrentLimits:   limits,
			OpenAlerts:      openAlerts,
		}
		if resp.Anomalies == nil {
			resp.Anomalies = []insights.Anomaly{}
		}
		if resp.Recommendations == nil {
			resp.Recommendations = []insights.Recommendation{}
		}
		resp.SuggestedLimits = insights.ApplyRecommendations(limits, resp.Recommendations)
		if stats, ok := insights.Summarize(samples); ok {
			resp.Stats = &stats
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// alertRuleRequest is the create/update body for a container alert rule.
type alertRuleRequest struct {
	Name            string   `json:"name"`
	ServerID        *string  `json:"serverId"`
	ContainerName   *string  `json:"containerName"`
	Metric          string   `json:"metric"`
	Threshold       *float64 `json:"threshold"`
	DurationSeconds *int     `json:"durationSeconds"`
	Severity        string   `json:"severity"`
	Enabled         *bool    `json:"enabled"`
}

// toRule validates req and converts it to a rule, applying defaults
// (5 minute duration, warning severity, enabled). Returns a user-facing
// error message on invalid input.
func (req alertRuleRequest) toRule() (store.ContainerAlertRule, string) {
	rule := store.ContainerAlertRule{
		Name:            strings.TrimSpace(req.Name),
		ServerID:        nonEmpty(req.ServerID),
		ContainerName:   nonEmpty(req.ContainerName),
		Metric:          req.Metric,
		DurationSeconds: 300,
		Severity:        req.Severity,
		Enabled:         true,
	}
	if rule.Name == "" {
		return rule, "name is required"
	}
	if !insights.ValidMetric(rule.Metric) {
		return rule, `metric must be "cpu", "memory", or "pids"`
	}
	if req.Threshold == nil || *req.Threshold < 0 {
		return rule, "threshold is required and must be non-negative"
	}
	rule.Threshold = *req.Threshold
	if rule.Metric == insights.MetricMemory && rule.Threshold > 100 {
		return rule, "a memory threshold is a percentage of the limit, so at most 100"
	}
	if req.DurationSeconds != nil {
		if *req.DurationSeconds < 0 || time.Duration(*req.DurationSeconds)*time.Second > maxAlertRuleDuration {
			return rule, "durationSeconds must be between 0 and " + strconv.Itoa(int(maxAlertRuleDuration.Seconds()))
		}
		rule.DurationSeconds = *req.DurationSeconds
	}
	if rule.Severity == "" {
		rule.Severity = insights.SeverityWarning
	}
	if rule.Severity != insights.SeverityWarning && rule.Severity != insights.SeverityCritical {
		return rule, `severity must be "warning" or "critical"`
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	return rule, ""
}

func nonEmpty(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	v := strings.TrimSpace(*s)
	return &v
}

func handleListContainerAlertRules(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rules, err := st.ListContainerAlertRules(r.Context(), false)
		if err != nil {
			log.Error("failed to list container alert rules", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, rules)
	}
}

func handleCreateContainerAlertRule(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req alertRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		rule, msg := req.toRule()
		if msg != "" {
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
			rule.CreatedBy = &claims.UserID
		}
		id, err := st.CreateContainerAlertRule(r.Context(), rule)
		if err != nil {
			log.Error("failed to create container alert rule", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}
}

func handleUpdateContainerAlertRule(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req alertRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		rule, msg := req.toRule()
		if msg != "" {
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		rule.ID = id
		if err := st.UpdateContainerAlertRule(r.Context(), rule); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "alert rule not found", http.StatusNotFound)
				return
			}
			log.Error("failed to update container alert rule", "rule_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteContainerAlertRule(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := st.DeleteContainerAlertRule(r.Context(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "alert rule not found", http.StatusNotFound)
				return
			}
			log.Error("failed to delete container alert rule", "rule_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListContainerAlerts lists alerts: ?status=open (default) or all,
// optionally narrowed by ?serverId= / ?containerId=.
func handleListContainerAlerts(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		status := q.Get("status")
		if status != "" && status != "open" && status != "all" {
			http.Error(w, `status must be "open" or "all"`, http.StatusBadRequest)
			return
		}
		alerts, err := st.ListContainerAlerts(r.Context(), store.ContainerAlertFilter{
			OpenOnly:    status != "all",
			ServerID:    q.Get("serverId"),
			ContainerID: q.Get("containerId"),
			Limit:       200,
		})
		if err != nil {
			log.Error("failed to list container alerts", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, alerts)
	}
}

func handleAcknowledgeContainerAlert(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := st.AcknowledgeContainerAlert(r.Context(), id, claims.UserID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "alert not found", http.StatusNotFound)
				return
			}
			log.Error("failed to acknowledge container alert", "alert_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
