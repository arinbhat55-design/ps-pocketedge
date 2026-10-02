-- User-managed Docker Compose files for the Deployment Management >
-- Docker Compose section — distinct from the admin-curated `stacks`
-- catalog (0001_init/0002_catalog), which is a fixed one-click-deploy
-- template list rather than something users author and edit themselves.
CREATE TABLE compose_files (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  content TEXT NOT NULL,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_compose_files_name ON compose_files(name);
