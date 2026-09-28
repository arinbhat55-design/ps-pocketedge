-- Threshold alert rules for container resource usage: "alert when metric
-- stays above threshold for duration_seconds". server_id/container_name
-- narrow the scope; NULL means every server / every container. Containers
-- are matched by name so a rule survives the container being recreated.
CREATE TABLE container_alert_rules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  server_id UUID REFERENCES servers(id) ON DELETE CASCADE,
  container_name TEXT,
  metric TEXT NOT NULL CHECK (metric IN ('cpu', 'memory', 'pids')),
  threshold DOUBLE PRECISION NOT NULL CHECK (threshold >= 0),
  duration_seconds INTEGER NOT NULL DEFAULT 300 CHECK (duration_seconds >= 0),
  severity TEXT NOT NULL DEFAULT 'warning' CHECK (severity IN ('warning', 'critical')),
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_by UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One firing (resolved_at NULL) or past alert on one container, raised
-- either by a rule (kind 'threshold') or by built-in abnormal-usage
-- detection (kind 'anomaly', anomaly_kind 'spike'/'leak'). Deleting a rule
-- keeps its alert history (rule_id becomes NULL).
CREATE TABLE container_alerts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind TEXT NOT NULL CHECK (kind IN ('threshold', 'anomaly')),
  rule_id UUID REFERENCES container_alert_rules(id) ON DELETE SET NULL,
  anomaly_kind TEXT,
  server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  container_id TEXT NOT NULL,
  container_name TEXT NOT NULL,
  metric TEXT NOT NULL,
  severity TEXT NOT NULL,
  threshold DOUBLE PRECISION,
  value DOUBLE PRECISION NOT NULL,
  message TEXT NOT NULL,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ,
  acknowledged_at TIMESTAMPTZ,
  acknowledged_by UUID REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_container_alerts_open ON container_alerts(started_at) WHERE resolved_at IS NULL;
CREATE INDEX idx_container_alerts_container ON container_alerts(server_id, container_id, started_at);

-- The alert evaluator loads recent samples fleet-wide every minute.
CREATE INDEX idx_container_metric_samples_recorded_at ON container_metric_samples(recorded_at);
