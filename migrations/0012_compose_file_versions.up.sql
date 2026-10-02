-- Compose file version history: every UpdateComposeFile call snapshots the
-- row's pre-update name/content here before overwriting it, so past
-- versions stay retrievable (view, compare, restore) — see store's
-- UpdateComposeFile doc comment.
ALTER TABLE compose_files
  ADD COLUMN version INT NOT NULL DEFAULT 1;

CREATE TABLE compose_file_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  compose_file_id UUID NOT NULL REFERENCES compose_files(id) ON DELETE CASCADE,
  version_number INT NOT NULL,
  name TEXT NOT NULL,
  content TEXT NOT NULL,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_compose_file_versions_file_id ON compose_file_versions(compose_file_id, version_number DESC);
