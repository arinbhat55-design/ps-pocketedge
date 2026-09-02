-- Deployment Management > Docker Compose > Configuration management:
-- reusable, named sets of environment variables tagged to an
-- environment/profile (development/test/staging/production), selectable
-- when deploying a Compose file instead of typing env vars in by hand
-- every time.
CREATE TABLE env_var_groups (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  environment TEXT NOT NULL DEFAULT 'development',
  variables JSONB NOT NULL DEFAULT '[]',
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_env_var_groups_environment ON env_var_groups(environment);
