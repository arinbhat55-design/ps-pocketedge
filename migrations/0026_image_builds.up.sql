-- Building images from Git: a Git-linked Compose file's services can use
-- build: instead of image:. Each rollout builds (or reuses) one image per
-- such service on the deployment's server, tagged from the commit and the
-- build settings, then deploys the Compose content with image: pinned to
-- those tags.

-- One image build on one server. The build settings are kept so the same
-- image can be rebuilt later (a rollback or promotion to a server that no
-- longer has it); build_args may hold vault references, resolved only on
-- the way to the agent. log is the build output, capped.
CREATE TABLE image_builds (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  revision INT NOT NULL,
  server_id UUID NOT NULL,
  service TEXT NOT NULL,
  image_tag TEXT NOT NULL,
  git_repository_id UUID REFERENCES git_repositories(id) ON DELETE SET NULL,
  git_ref TEXT NOT NULL DEFAULT '',
  git_commit TEXT NOT NULL,
  context_path TEXT NOT NULL,
  dockerfile TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  build_args JSONB NOT NULL DEFAULT '{}',
  labels JSONB NOT NULL DEFAULT '{}',
  no_cache BOOLEAN NOT NULL DEFAULT false,
  -- queued, cloning, building, succeeded, failed, cancelled, superseded
  status TEXT NOT NULL DEFAULT 'queued',
  status_message TEXT NOT NULL DEFAULT '',
  reused BOOLEAN NOT NULL DEFAULT false,
  image_id TEXT NOT NULL DEFAULT '',
  log TEXT NOT NULL DEFAULT '',
  log_seq BIGINT NOT NULL DEFAULT 0,
  created_by UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ
);

CREATE INDEX idx_image_builds_deployment ON image_builds(deployment_id, created_at DESC);
CREATE INDEX idx_image_builds_image_tag ON image_builds(image_tag);
CREATE INDEX idx_image_builds_active ON image_builds(deployment_id) WHERE status IN ('queued', 'cloning', 'building');

-- source_content is the Compose content as written (with build:) when
-- compose_content had build: replaced by pinned image tags; empty
-- otherwise. change_summary describes what the revision changed compared
-- with the one before it.
ALTER TABLE deployment_revisions
  ADD COLUMN source_content TEXT NOT NULL DEFAULT '',
  ADD COLUMN change_summary TEXT NOT NULL DEFAULT '';
