-- Preserve when work happened separately from when it was recorded.
ALTER TABLE worklogs ADD COLUMN started_at TIMESTAMPTZ;
ALTER TABLE worklogs ADD COLUMN updated_by TEXT;
UPDATE worklogs SET started_at=created_at, updated_by=author_id;
ALTER TABLE worklogs ALTER COLUMN started_at SET DEFAULT now();
ALTER TABLE worklogs ALTER COLUMN started_at SET NOT NULL;
ALTER TABLE worklogs ALTER COLUMN updated_by SET NOT NULL;
CREATE INDEX idx_worklogs_started ON worklogs(issue_id, started_at, id);
