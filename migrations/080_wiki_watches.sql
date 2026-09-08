CREATE TABLE wiki_watches (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  target_type TEXT NOT NULL CHECK (target_type IN ('content','space','label')),
  target_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id,user_id,target_type,target_id)
);

CREATE INDEX wiki_watches_target
  ON wiki_watches(workspace_id,target_type,target_id,created_at,user_id);
