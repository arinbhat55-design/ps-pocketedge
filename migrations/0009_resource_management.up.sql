-- Per-container resource-usage history, the container-scoped counterpart to
-- server_metric_samples (0004_health). Sampled less often than the server-
-- wide snapshot (see agent/stream/session.go's collectContainerStats) since
-- a per-container ContainerStats call is more expensive than the host-level
-- sampling that feeds server_metric_samples.
CREATE TABLE container_metric_samples (
  id BIGSERIAL PRIMARY KEY,
  server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  container_id TEXT NOT NULL,
  recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  cpu_percent DOUBLE PRECISION NOT NULL,
  mem_usage_bytes BIGINT NOT NULL,
  mem_limit_bytes BIGINT NOT NULL,
  mem_percent DOUBLE PRECISION NOT NULL,
  net_rx_bytes BIGINT NOT NULL,
  net_tx_bytes BIGINT NOT NULL,
  block_read_bytes BIGINT NOT NULL,
  block_write_bytes BIGINT NOT NULL,
  pids BIGINT NOT NULL
);

CREATE INDEX idx_container_metric_samples_container_time ON container_metric_samples(server_id, container_id, recorded_at);
