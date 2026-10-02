-- Lets a deployment originate from a user-authored compose_files row
-- instead of only the curated stacks catalog. dispatchDeploy (control
-- plane) and Deploy (agent) already only need a name + compose YAML, so
-- this is purely a "where did the YAML come from" distinction — the
-- deploy path itself is unchanged.
ALTER TABLE deployments
  ALTER COLUMN stack_id DROP NOT NULL,
  ADD COLUMN compose_file_id UUID REFERENCES compose_files(id);

ALTER TABLE deployments
  ADD CONSTRAINT deployments_exactly_one_source CHECK (
    (stack_id IS NOT NULL)::int + (compose_file_id IS NOT NULL)::int = 1
  );

CREATE INDEX idx_deployments_compose_file_id ON deployments(compose_file_id);
