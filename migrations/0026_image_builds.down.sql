ALTER TABLE deployment_revisions
  DROP COLUMN change_summary,
  DROP COLUMN source_content;

DROP TABLE image_builds;
