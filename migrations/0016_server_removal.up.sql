-- Removing a server retires it rather than deleting the row: deployments
-- and backups keep a required reference to the server they ran on, so a
-- hard delete would either be blocked or erase that history. A retired
-- server (removed_at set) is hidden from listings and can no longer
-- authenticate its agent; its container snapshot and metrics are deleted
-- at removal time (see store.RetireServer).
ALTER TABLE servers ADD COLUMN removed_at TIMESTAMPTZ;
