CREATE TABLE access_settings (
  id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
  require_local_login BOOLEAN NOT NULL DEFAULT FALSE
);
INSERT INTO access_settings (id, require_local_login) VALUES (TRUE, FALSE);
