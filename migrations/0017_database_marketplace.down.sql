DROP INDEX IF EXISTS idx_backups_deployment_created;
ALTER TABLE backups DROP COLUMN IF EXISTS origin;
ALTER TABLE secrets DROP COLUMN IF EXISTS database_id;
DROP TABLE IF EXISTS database_instances;
DROP TABLE IF EXISTS secret_grants;
DROP TABLE IF EXISTS secrets;
