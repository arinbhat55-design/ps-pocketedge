CREATE TABLE database_clone_jobs (
  target_database_id UUID PRIMARY KEY REFERENCES database_instances(id) ON DELETE CASCADE,
  backup_id UUID NOT NULL REFERENCES backups(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','dispatched','completed','failed')),
  message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
