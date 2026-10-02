DELETE FROM compose_files
WHERE deleted_at IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM deployments d WHERE d.compose_file_id = compose_files.id);

-- Soft-deleted files still referenced by removed deployments may share a
-- name with a live file; suffix them so the plain unique index fits.
UPDATE compose_files SET name = name || ' (deleted ' || id || ')' WHERE deleted_at IS NOT NULL;

DROP INDEX idx_compose_files_name;
CREATE UNIQUE INDEX idx_compose_files_name ON compose_files(name);
ALTER TABLE compose_files DROP COLUMN deleted_at;
