CREATE TABLE backups (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  deployment_id UUID NOT NULL REFERENCES deployments(id),
  server_id UUID NOT NULL REFERENCES servers(id),
  status TEXT NOT NULL DEFAULT 'pending', -- pending, running, completed, failed
  message TEXT,
  size_bytes BIGINT,
  storage_path TEXT,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

CREATE INDEX idx_backups_deployment_id ON backups(deployment_id);
