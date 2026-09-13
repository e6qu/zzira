-- An admin key gives an organization or site administrator temporary access to
-- all content, including content restricted to other people. Without one, an
-- administrator sees restricted content only as any other person would. A key
-- lasts ten minutes unless asked for longer, and never more than an hour.
CREATE TABLE wiki_admin_keys (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id)
);
