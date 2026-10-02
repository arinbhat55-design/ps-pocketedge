ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'viewer'));
