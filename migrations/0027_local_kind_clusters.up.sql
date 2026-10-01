ALTER TABLE kubernetes_clusters
  ADD COLUMN local_kind_name TEXT NOT NULL DEFAULT '';
