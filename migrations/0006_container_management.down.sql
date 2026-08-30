DROP INDEX IF EXISTS idx_server_containers_image;
DROP INDEX IF EXISTS idx_server_containers_deployment_id;
ALTER TABLE server_containers
  DROP COLUMN IF EXISTS mounts,
  DROP COLUMN IF EXISTS networks,
  DROP COLUMN IF EXISTS ports,
  DROP COLUMN IF EXISTS status,
  DROP COLUMN IF EXISTS created_at,
  DROP COLUMN IF EXISTS image_id,
  DROP COLUMN IF EXISTS image;

DROP INDEX IF EXISTS idx_deployments_tags;
DROP INDEX IF EXISTS idx_deployments_deploy_environment;
DROP INDEX IF EXISTS idx_deployments_created_by;
ALTER TABLE deployments
  DROP COLUMN IF EXISTS tags,
  DROP COLUMN IF EXISTS deploy_environment;
