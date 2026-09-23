-- Deployment Management > Docker Compose: the remaining stack deployment,
-- Git-based deployment, and governance features.

-- Per-environment governance policy — "Development, test, and production
-- environments", "Approval workflow", "Maintenance window". One row per
-- supported environment (see store.IsValidEnvironment). maintenance_windows
-- is a JSON array of {"days":[0..6], "start":"HH:MM", "end":"HH:MM"} in
-- UTC, Sunday = 0; a window whose end is before its start wraps past
-- midnight.
CREATE TABLE environment_policies (
  environment TEXT PRIMARY KEY,
  require_approval BOOLEAN NOT NULL DEFAULT false,
  allow_self_approval BOOLEAN NOT NULL DEFAULT true,
  require_change_request BOOLEAN NOT NULL DEFAULT false,
  require_rollback_plan BOOLEAN NOT NULL DEFAULT false,
  enforce_maintenance_window BOOLEAN NOT NULL DEFAULT false,
  maintenance_windows JSONB NOT NULL DEFAULT '[]',
  updated_by UUID REFERENCES users(id),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO environment_policies (environment, require_approval) VALUES
  ('development', false),
  ('test', false),
  ('staging', false),
  ('production', true);

-- Git repositories Compose files can be imported from and kept in sync
-- with. token is stored in plaintext, the same tradeoff (and the same
-- admin-only access) as registries.password; it's never returned by the
-- API. webhook_secret authenticates inbound push webhooks.
CREATE TABLE git_repositories (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL UNIQUE,
  provider TEXT NOT NULL DEFAULT 'generic',
  url TEXT NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  token TEXT NOT NULL DEFAULT '',
  default_branch TEXT NOT NULL DEFAULT 'main',
  webhook_secret TEXT NOT NULL,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A Compose file can be linked to one path at one ref (branch or tag) in a
-- Git repository; git_commit is the commit its current content came from.
ALTER TABLE compose_files
  ADD COLUMN git_repository_id UUID REFERENCES git_repositories(id) ON DELETE SET NULL,
  ADD COLUMN git_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN git_path TEXT NOT NULL DEFAULT '',
  ADD COLUMN git_commit TEXT NOT NULL DEFAULT '',
  ADD COLUMN git_synced_at TIMESTAMPTZ;

ALTER TABLE compose_file_versions
  ADD COLUMN git_commit TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_compose_files_git_repository_id ON compose_files(git_repository_id);

ALTER TABLE deployments
  -- Stack deployment: replica overrides per service ({"web": 3}) and the
  -- rollout strategy ('recreate' or 'rolling').
  ADD COLUMN scales JSONB NOT NULL DEFAULT '{}',
  ADD COLUMN update_strategy TEXT NOT NULL DEFAULT 'recreate',
  -- Git-based deployment: the branch/tag this deployment tracks (empty =
  -- the Compose file's own ref), and whether a push webhook redeploys it.
  ADD COLUMN git_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN auto_deploy BOOLEAN NOT NULL DEFAULT false,
  -- Governance: free-text rollback plan, plus automatic rollback to the
  -- last healthy revision when post-deployment verification fails.
  ADD COLUMN rollback_plan TEXT NOT NULL DEFAULT '',
  ADD COLUMN auto_rollback BOOLEAN NOT NULL DEFAULT false,
  -- Post-deployment health verification result for the current revision:
  -- unknown, verifying, healthy, or unhealthy.
  ADD COLUMN health_status TEXT NOT NULL DEFAULT 'unknown',
  ADD COLUMN health_message TEXT NOT NULL DEFAULT '',
  ADD COLUMN health_checked_at TIMESTAMPTZ,
  ADD COLUMN current_revision INT NOT NULL DEFAULT 0,
  -- Set on a deployment created by promoting another one.
  ADD COLUMN promoted_from UUID REFERENCES deployments(id) ON DELETE SET NULL;

-- One row per rollout actually dispatched to an agent: the exact Compose
-- content, env, and Git commit it ran with. Backs "Deployment history",
-- "Display commit ID associated with each deployment", rolling back to a
-- previous revision/commit, and config drift detection.
CREATE TABLE deployment_revisions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  revision INT NOT NULL,
  action TEXT NOT NULL,
  compose_content TEXT NOT NULL,
  env JSONB NOT NULL DEFAULT '{}',
  scales JSONB NOT NULL DEFAULT '{}',
  compose_version INT,
  git_ref TEXT NOT NULL DEFAULT '',
  git_commit TEXT NOT NULL DEFAULT '',
  strategy TEXT NOT NULL DEFAULT 'recreate',
  -- dispatched, running, failed, healthy, unhealthy
  status TEXT NOT NULL DEFAULT 'dispatched',
  status_message TEXT NOT NULL DEFAULT '',
  auto_rollback_attempted BOOLEAN NOT NULL DEFAULT false,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (deployment_id, revision)
);

-- A change to a deployment that couldn't run immediately: it's waiting for
-- approval, or approved but waiting for the next maintenance window.
-- status: pending_approval, scheduled, executed, rejected, cancelled, failed.
CREATE TABLE deployment_requests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  action TEXT NOT NULL,
  params JSONB NOT NULL DEFAULT '{}',
  status TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  requested_by UUID REFERENCES users(id),
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  decided_by UUID REFERENCES users(id),
  decided_at TIMESTAMPTZ,
  decision_comment TEXT NOT NULL DEFAULT '',
  scheduled_for TIMESTAMPTZ,
  executed_at TIMESTAMPTZ,
  result_message TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_deployment_requests_status ON deployment_requests(status);
CREATE INDEX idx_deployment_requests_deployment_id ON deployment_requests(deployment_id);

-- Per-service progress: which service a deployment event is about (empty
-- for whole-stack events).
ALTER TABLE deployment_events
  ADD COLUMN service TEXT NOT NULL DEFAULT '';

-- "Complete audit trail": every user-initiated change across Compose files,
-- variable groups, deployments, approvals, policies, and Git repositories.
CREATE TABLE audit_events (
  id BIGSERIAL PRIMARY KEY,
  actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  details JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_events_entity ON audit_events(entity_type, entity_id);
CREATE INDEX idx_audit_events_created_at ON audit_events(created_at DESC);
