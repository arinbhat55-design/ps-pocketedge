-- Deployment Management > Docker Compose > Governance:
-- change_request/notes are free-text metadata a deployer can attach at
-- create time or edit later (see UpdateDeploymentMetadata), same editing
-- model as the existing deploy_environment/tags. triggered_by records
-- which user performed a manual action (create/redeploy/rollback/stack
-- action) — NULL for agent-reported phase events, which have no human
-- actor — "Complete audit trail".
ALTER TABLE deployments
  ADD COLUMN change_request TEXT NOT NULL DEFAULT '',
  ADD COLUMN notes TEXT NOT NULL DEFAULT '';

ALTER TABLE deployment_events
  ADD COLUMN triggered_by UUID REFERENCES users(id);
