CREATE TABLE server_metric_samples (
  id BIGSERIAL PRIMARY KEY,
  server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  cpu_percent DOUBLE PRECISION NOT NULL,
  mem_percent DOUBLE PRECISION NOT NULL,
  disk_percent DOUBLE PRECISION NOT NULL
);

CREATE INDEX idx_server_metric_samples_server_time ON server_metric_samples(server_id, recorded_at);

-- Reflects current container state only, replaced in full on every
-- heartbeat — not a time-series table (see server_metric_samples for that).
CREATE TABLE server_containers (
  server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  container_id TEXT NOT NULL,
  name TEXT NOT NULL,
  state TEXT NOT NULL,
  deployment_id UUID,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (server_id, container_id)
);
