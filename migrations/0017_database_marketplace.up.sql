-- Database Marketplace: databases deployed from the curated engine catalog
-- (internal/controlplane/dbcatalog), plus the credential vault their
-- secrets live in.
--
-- A database instance is a thin record on top of an ordinary deployment:
-- the wizard renders a Compose file (compose_files row, so it gets
-- versioning and shows up in Deployment Management like any other) and
-- deploys it through the normal governance path. What this table adds is
-- the engine-level knowledge the generic deployment doesn't have — which
-- engine/version, who the admin user is, where its credentials are, and
-- its backup policy.

-- Vault secrets. value_ciphertext is AES-256-GCM (see internal/controlplane
-- /vault); the plaintext never touches the database, deployment env, or
-- Compose content — those hold a "vault:<id>" reference instead, resolved
-- only when a command is dispatched to an agent.
CREATE TABLE secrets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  -- admin (the instance's primary credential), token (an extra generated
  -- key such as InfluxDB's admin token), or temporary (a short-lived
  -- database user issued on demand).
  kind TEXT NOT NULL CHECK (kind IN ('admin', 'token', 'temporary')),
  -- The login name the secret belongs to, if any. Not secret itself —
  -- stored in the clear so listings can show it without decrypting.
  username TEXT NOT NULL DEFAULT '',
  value_ciphertext BYTEA NOT NULL,
  version INT NOT NULL DEFAULT 1,
  owner_id UUID REFERENCES users(id) ON DELETE SET NULL,
  expires_at TIMESTAMPTZ,
  -- Set once the one-time credentials file has been downloaded; a second
  -- download is refused.
  downloaded_at TIMESTAMPTZ,
  rotated_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Explicit per-user access to a secret, beyond its owner and admins.
-- expires_at makes a share temporary.
CREATE TABLE secret_grants (
  secret_id UUID NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  granted_by UUID REFERENCES users(id) ON DELETE SET NULL,
  expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (secret_id, user_id)
);

CREATE TABLE database_instances (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  engine TEXT NOT NULL,
  version TEXT NOT NULL,
  deployment_id UUID NOT NULL UNIQUE REFERENCES deployments(id) ON DELETE CASCADE,
  compose_file_id UUID REFERENCES compose_files(id) ON DELETE SET NULL,
  server_id UUID NOT NULL REFERENCES servers(id),
  -- The Compose service clients connect to; rotation and temporary-user
  -- commands run in its first replica.
  primary_service TEXT NOT NULL,
  database_name TEXT NOT NULL DEFAULT '',
  admin_username TEXT NOT NULL DEFAULT '',
  -- The primary service's host port and access mode (local = bound to
  -- 127.0.0.1 on the server, remote = all interfaces).
  port INT NOT NULL,
  access TEXT NOT NULL CHECK (access IN ('local', 'remote')),
  profile TEXT NOT NULL CHECK (profile IN ('development', 'production')),
  storage_gb INT NOT NULL DEFAULT 0,
  memory_mb INT NOT NULL DEFAULT 0,
  cpus NUMERIC(5, 2) NOT NULL DEFAULT 0,
  high_availability BOOLEAN NOT NULL DEFAULT false,
  admin_secret_id UUID REFERENCES secrets(id) ON DELETE SET NULL,
  -- Backup policy. backup_cron is a standard 5-field cron in UTC; NULL
  -- means no scheduled backups. Retention prunes this instance's
  -- completed backups older than retention_days and beyond
  -- retention_count (0 = no limit on that axis).
  backup_cron TEXT,
  backup_next_run_at TIMESTAMPTZ,
  backup_last_run_at TIMESTAMPTZ,
  backup_last_status TEXT,
  backup_consistent BOOLEAN NOT NULL DEFAULT true,
  retention_days INT NOT NULL DEFAULT 0,
  retention_count INT NOT NULL DEFAULT 0,
  created_by UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_database_instances_backup_due ON database_instances(backup_next_run_at) WHERE backup_cron IS NOT NULL;

-- Which instance a secret belongs to. A separate column rather than a
-- database_instances FK list so one query finds every secret to redact
-- from an instance's logs.
ALTER TABLE secrets ADD COLUMN database_id UUID REFERENCES database_instances(id) ON DELETE CASCADE;
CREATE INDEX idx_secrets_database_id ON secrets(database_id);
CREATE INDEX idx_secrets_expiring ON secrets(expires_at) WHERE expires_at IS NOT NULL AND revoked_at IS NULL;

-- Scheduled backups have no user behind them; allow created_by to be
-- empty (it already is nullable) and record why a backup was taken.
ALTER TABLE backups ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual';
CREATE INDEX idx_backups_deployment_created ON backups(deployment_id, created_at);
