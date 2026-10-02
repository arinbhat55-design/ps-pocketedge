ALTER TABLE deployment_events DROP COLUMN IF EXISTS triggered_by;
ALTER TABLE deployments
  DROP COLUMN IF EXISTS change_request,
  DROP COLUMN IF EXISTS notes;
