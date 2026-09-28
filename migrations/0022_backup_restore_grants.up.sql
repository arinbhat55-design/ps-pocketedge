CREATE TABLE backup_restore_grants (
  backup_id UUID NOT NULL REFERENCES backups(id) ON DELETE CASCADE,
  server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (backup_id, server_id)
);
