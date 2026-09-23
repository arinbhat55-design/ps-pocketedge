DROP TABLE IF EXISTS audit_events;
ALTER TABLE deployment_events DROP COLUMN IF EXISTS service;
DROP TABLE IF EXISTS deployment_requests;
DROP TABLE IF EXISTS deployment_revisions;
ALTER TABLE deployments
  DROP COLUMN IF EXISTS scales,
  DROP COLUMN IF EXISTS update_strategy,
  DROP COLUMN IF EXISTS git_ref,
  DROP COLUMN IF EXISTS auto_deploy,
  DROP COLUMN IF EXISTS rollback_plan,
  DROP COLUMN IF EXISTS auto_rollback,
  DROP COLUMN IF EXISTS health_status,
  DROP COLUMN IF EXISTS health_message,
  DROP COLUMN IF EXISTS health_checked_at,
  DROP COLUMN IF EXISTS current_revision,
  DROP COLUMN IF EXISTS promoted_from;
ALTER TABLE compose_file_versions DROP COLUMN IF EXISTS git_commit;
DROP INDEX IF EXISTS idx_compose_files_git_repository_id;
ALTER TABLE compose_files
  DROP COLUMN IF EXISTS git_repository_id,
  DROP COLUMN IF EXISTS git_ref,
  DROP COLUMN IF EXISTS git_path,
  DROP COLUMN IF EXISTS git_commit,
  DROP COLUMN IF EXISTS git_synced_at;
DROP TABLE IF EXISTS git_repositories;
DROP TABLE IF EXISTS environment_policies;
