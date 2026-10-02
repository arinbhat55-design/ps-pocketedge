-- Sessions (JWTs) carry the session_version they were issued under and are
-- rejected once it changes. A password change bumps it, so a reset signs
-- out every existing session.
ALTER TABLE users ADD COLUMN session_version INT NOT NULL DEFAULT 1;
