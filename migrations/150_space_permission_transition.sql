-- Moving a site from direct space permission grants to roles starts by finding
-- the distinct sets of permissions people actually hold. Each distinct set is a
-- combination, and an administrator decides once per combination what role it
-- should become.
-- A combination is named by the permissions it contains, so the same set in two
-- workspaces has the same name; the workspace is part of the key.
CREATE TABLE wiki_space_permission_combinations (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  permissions TEXT[] NOT NULL,
  space_count INTEGER NOT NULL,
  principal_count INTEGER NOT NULL,
  principal_types TEXT[] NOT NULL,
  generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id)
);
CREATE INDEX wiki_space_permission_combinations_workspace
  ON wiki_space_permission_combinations(workspace_id, id);
