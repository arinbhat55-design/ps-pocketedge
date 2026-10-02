-- Removed deployments keep pointing at the compose file they ran (their
-- revisions, events and backups are history worth keeping), so a compose
-- file that only removed deployments still use is hidden rather than
-- deleted. Its name becomes free for a new file.
ALTER TABLE compose_files ADD COLUMN deleted_at TIMESTAMPTZ;

DROP INDEX idx_compose_files_name;
CREATE UNIQUE INDEX idx_compose_files_name ON compose_files(name) WHERE deleted_at IS NULL;
