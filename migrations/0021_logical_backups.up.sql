ALTER TABLE backups ADD COLUMN format TEXT NOT NULL DEFAULT 'volumes'
  CHECK (format IN ('volumes', 'postgres_custom'));
