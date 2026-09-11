-- Jira trashes a custom field before deleting it, and a trashed field can be
-- restored with its data intact, so the state is a column rather than a delete.
ALTER TABLE custom_fields ADD COLUMN trashed_at TIMESTAMPTZ;
ALTER TABLE custom_fields ADD COLUMN searcher_key TEXT NOT NULL DEFAULT '';
CREATE INDEX custom_fields_workspace_trashed ON custom_fields(workspace_id, trashed_at, id);
