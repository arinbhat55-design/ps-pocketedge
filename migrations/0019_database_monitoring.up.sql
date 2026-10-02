CREATE TABLE database_metric_samples (
  database_id UUID NOT NULL REFERENCES database_instances(id) ON DELETE CASCADE,
  recorded_at TIMESTAMPTZ NOT NULL,
  connection_count INT NOT NULL,
  transactions_total BIGINT NOT NULL,
  cache_hit_ratio DOUBLE PRECISION NOT NULL,
  deadlocks_total BIGINT NOT NULL,
  lock_wait_count INT NOT NULL,
  database_bytes BIGINT NOT NULL,
  active_query_count INT NOT NULL,
  long_running_transaction_count INT NOT NULL,
  replication_lag_seconds DOUBLE PRECISION,
  PRIMARY KEY (database_id, recorded_at)
);

CREATE TABLE database_alerts (
  id BIGSERIAL PRIMARY KEY,
  database_id UUID NOT NULL REFERENCES database_instances(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  severity TEXT NOT NULL,
  message TEXT NOT NULL,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_database_alerts_active ON database_alerts(database_id, kind) WHERE resolved_at IS NULL;
