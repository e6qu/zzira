ALTER TABLE projects
  ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE'
    CHECK (lifecycle_state IN ('ACTIVE', 'ARCHIVED', 'TRASHED')),
  ADD COLUMN archived_at TIMESTAMPTZ,
  ADD COLUMN trashed_at TIMESTAMPTZ,
  ADD COLUMN lifecycle_actor_id TEXT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX projects_workspace_lifecycle_idx
  ON projects (workspace_id, lifecycle_state, key);

CREATE TABLE project_views (
  workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  last_viewed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id, project_id)
);

CREATE INDEX project_views_recent_idx
  ON project_views (workspace_id, user_id, last_viewed_at DESC, project_id);

ALTER TABLE issues DROP CONSTRAINT issues_project_id_fkey;
ALTER TABLE issues
  ADD CONSTRAINT issues_project_id_fkey FOREIGN KEY (project_id)
  REFERENCES projects(id) ON DELETE CASCADE;

ALTER TABLE issues DROP CONSTRAINT issues_parent_id_fkey;
ALTER TABLE issues
  ADD CONSTRAINT issues_parent_id_fkey FOREIGN KEY (parent_id)
  REFERENCES issues(id) ON DELETE SET NULL;

ALTER TABLE boards DROP CONSTRAINT boards_project_id_fkey;
ALTER TABLE boards
  ADD CONSTRAINT boards_project_id_fkey FOREIGN KEY (project_id)
  REFERENCES projects(id) ON DELETE CASCADE;

ALTER TABLE sprints DROP CONSTRAINT sprints_board_id_fkey;
ALTER TABLE sprints
  ADD CONSTRAINT sprints_board_id_fkey FOREIGN KEY (board_id)
  REFERENCES boards(id) ON DELETE CASCADE;

ALTER TABLE sprint_issues DROP CONSTRAINT sprint_issues_sprint_id_fkey;
ALTER TABLE sprint_issues
  ADD CONSTRAINT sprint_issues_sprint_id_fkey FOREIGN KEY (sprint_id)
  REFERENCES sprints(id) ON DELETE CASCADE;
