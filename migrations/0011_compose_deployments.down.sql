ALTER TABLE deployments
  DROP CONSTRAINT IF EXISTS deployments_exactly_one_source,
  DROP COLUMN IF EXISTS compose_file_id,
  ALTER COLUMN stack_id SET NOT NULL;
