CREATE TABLE wiki_tasks (
  id BIGSERIAL PRIMARY KEY,
  page_id BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
  local_id TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'incomplete' CHECK (status IN ('complete', 'incomplete')),
  created_by TEXT NOT NULL REFERENCES users(id),
  assigned_to TEXT REFERENCES users(id),
  completed_by TEXT REFERENCES users(id),
  due_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (page_id, local_id),
  CHECK (length(body) <= 1048576),
  CHECK ((status = 'complete' AND completed_by IS NOT NULL AND completed_at IS NOT NULL)
    OR (status = 'incomplete' AND completed_by IS NULL AND completed_at IS NULL))
);

CREATE INDEX wiki_tasks_page_status_due_idx
  ON wiki_tasks(page_id, status, due_at, id);
CREATE INDEX wiki_tasks_assignee_status_idx
  ON wiki_tasks(assigned_to, status, due_at, id);
