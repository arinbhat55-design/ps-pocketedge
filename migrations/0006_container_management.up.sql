-- deploy_environment is a free-text grouping tag (dev/staging/prod, etc.),
-- distinct from the existing env JSONB column which holds compose env vars.
ALTER TABLE deployments
  ADD COLUMN deploy_environment TEXT,
  ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';

CREATE INDEX idx_deployments_created_by ON deployments(created_by);
CREATE INDEX idx_deployments_deploy_environment ON deployments(deploy_environment);
CREATE INDEX idx_deployments_tags ON deployments USING GIN (tags);

-- Cheap per-container fields populated from the existing cli.ContainerList
-- call on every heartbeat that refreshes containers — no new Docker API
-- round-trip. ports/networks/mounts are display-only, never filtered or
-- joined on, so they're stored as JSONB rather than normalized tables.
ALTER TABLE server_containers
  ADD COLUMN image TEXT,
  ADD COLUMN image_id TEXT,
  ADD COLUMN created_at TIMESTAMPTZ,
  ADD COLUMN status TEXT,
  ADD COLUMN ports JSONB,
  ADD COLUMN networks JSONB,
  ADD COLUMN mounts JSONB;

CREATE INDEX idx_server_containers_deployment_id ON server_containers(deployment_id);
CREATE INDEX idx_server_containers_image ON server_containers(image);
