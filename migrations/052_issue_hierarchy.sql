ALTER TABLE issues
  ADD COLUMN IF NOT EXISTS parent_id TEXT REFERENCES issues(id);

CREATE INDEX IF NOT EXISTS idx_issues_parent ON issues (parent_id, status_id);

INSERT INTO issue_types (id, name, icon, subtask)
VALUES ('it_subtask', 'Sub-task', 'subtask', TRUE)
ON CONFLICT (id) DO UPDATE SET subtask=TRUE;
