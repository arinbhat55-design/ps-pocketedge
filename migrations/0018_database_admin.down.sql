DELETE FROM secrets WHERE kind = 'managed';
ALTER TABLE secrets DROP CONSTRAINT secrets_kind_check;
ALTER TABLE secrets ADD CONSTRAINT secrets_kind_check CHECK (kind IN ('admin', 'token', 'temporary'));
