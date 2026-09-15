-- A site has one wallboard slide show: the dashboards it cycles through, how
-- long each shows and whether they come in random order. Each viewer sees only
-- the dashboards of it they may view.
CREATE TABLE dashboard_wallboard_slideshows (
  workspace_id     TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  dashboard_ids    JSONB NOT NULL DEFAULT '[]',
  interval_seconds INTEGER NOT NULL DEFAULT 30 CHECK (interval_seconds BETWEEN 5 AND 3600),
  random_order     BOOLEAN NOT NULL DEFAULT FALSE,
  updated_by       TEXT NOT NULL,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
