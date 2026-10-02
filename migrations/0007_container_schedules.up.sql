CREATE TABLE container_schedules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  container_id TEXT NOT NULL,
  -- Denormalized for display; container_id has no FK — it's an
  -- agent/Docker-assigned id that isn't tracked as its own row, and can
  -- change out from under a schedule if the container is later recreated
  -- (a known limitation, not handled by this MVP slice).
  container_name TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('start', 'stop')),
  schedule_type TEXT NOT NULL CHECK (schedule_type IN ('recurring', 'once')),
  -- Exactly one of cron_expr/run_once_at is set, matching schedule_type.
  -- Everything here is UTC — the client converts local day/time picks to
  -- UTC before submitting.
  cron_expr TEXT,
  run_once_at TIMESTAMPTZ,
  next_run_at TIMESTAMPTZ NOT NULL,
  last_run_at TIMESTAMPTZ,
  last_run_status TEXT,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_container_schedules_due ON container_schedules(next_run_at) WHERE enabled;
CREATE INDEX idx_container_schedules_container ON container_schedules(server_id, container_id);
