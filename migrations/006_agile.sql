ALTER TABLE issues ADD COLUMN IF NOT EXISTS rank TEXT NOT NULL DEFAULT 'u';
CREATE INDEX IF NOT EXISTS idx_issues_rank ON issues (project_id, status_id, rank);

CREATE TABLE boards (
  id                TEXT PRIMARY KEY,
  project_id        TEXT NOT NULL REFERENCES projects(id),
  name              TEXT NOT NULL,
  type              TEXT NOT NULL DEFAULT 'scrum',
  column_status_ids TEXT[] NOT NULL DEFAULT '{st_todo,st_inprogress,st_done}',
  filter_jql        TEXT NOT NULL DEFAULT '',
  UNIQUE (project_id, name)
);

CREATE TABLE sprints (
  id         TEXT PRIMARY KEY,
  board_id   TEXT NOT NULL REFERENCES boards(id),
  name       TEXT NOT NULL,
  state      TEXT NOT NULL DEFAULT 'future',
  start_date TIMESTAMPTZ,
  end_date   TIMESTAMPTZ,
  goal       TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sprint_issues (
  sprint_id TEXT NOT NULL REFERENCES sprints(id),
  issue_id  TEXT NOT NULL REFERENCES issues(id),
  rank      TEXT NOT NULL DEFAULT 'u',
  PRIMARY KEY (sprint_id, issue_id)
);

CREATE TABLE watchers (
  issue_id TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  user_id  TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (issue_id, user_id)
);

CREATE TABLE notifications (
  id          TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  user_id     TEXT NOT NULL,
  actor_id    TEXT NOT NULL,
  kind        TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  message     TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user ON notifications (user_id, created_at DESC);

INSERT INTO boards (id, project_id, name, type, column_status_ids)
VALUES ('brd_default', 'prj_default', 'ZZ board', 'scrum', '{st_todo,st_inprogress,st_done}')
ON CONFLICT (id) DO NOTHING;
