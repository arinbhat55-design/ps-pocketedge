-- Private registry credentials, admin-managed. Plaintext password, matching
-- this codebase's existing security posture (e.g. JWT secret handling) —
-- an accepted MVP simplification, not an oversight.
CREATE TABLE registries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  password TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Approved-image allowlist. pattern matches an image reference by prefix
-- (e.g. "nginx", "myregistry.com/team/") or exact match; enforcement (see
-- image_policy_settings) is all-or-nothing, not per-environment.
CREATE TABLE approved_images (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pattern TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Single-row settings table (id enforced to 1) toggling whether
-- approved_images is actually enforced at deploy/create/recreate time.
CREATE TABLE image_policy_settings (
  id INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  enabled BOOLEAN NOT NULL DEFAULT false
);
INSERT INTO image_policy_settings (id, enabled) VALUES (1, false);

-- Per-container image history, so "rollback" can recreate a standalone
-- container on its previous image. container_id has no FK for the same
-- reason container_schedules.container_id doesn't — it's an
-- agent/Docker-assigned id, not tracked as its own row.
CREATE TABLE image_rollback_history (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  container_id TEXT NOT NULL,
  previous_image TEXT NOT NULL,
  captured_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_image_rollback_history_container ON image_rollback_history(server_id, container_id, captured_at DESC);

-- Vulnerability scan results (Trivy), keyed by the image reference scanned
-- (not a digest — a floating tag like "nginx:latest" is what gets
-- re-scanned over time, and raw_result/counts are simply overwritten by
-- the next scan of the same ref rather than versioned).
CREATE TABLE image_scans (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  image_ref TEXT NOT NULL,
  scanned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  critical_count INT NOT NULL DEFAULT 0,
  high_count INT NOT NULL DEFAULT 0,
  medium_count INT NOT NULL DEFAULT 0,
  low_count INT NOT NULL DEFAULT 0,
  unknown_count INT NOT NULL DEFAULT 0,
  raw_result JSONB,
  error TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX idx_image_scans_ref ON image_scans(image_ref);
