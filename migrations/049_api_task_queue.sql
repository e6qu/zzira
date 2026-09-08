ALTER TABLE api_tasks
  ADD COLUMN description TEXT NOT NULL DEFAULT '',
  ADD COLUMN kind TEXT NOT NULL DEFAULT '',
  ADD COLUMN payload JSONB;

CREATE INDEX api_tasks_claimable
  ON api_tasks(workspace_id,submitted_at,id)
  WHERE status IN ('ENQUEUED','RUNNING');
