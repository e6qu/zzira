-- Jira's enhanced search can be asked to include the work of archived
-- projects. The choice belongs to the snapshot: every page of a search
-- re-checks what the reader may see, and the second page must answer the
-- question the first page was asked.
ALTER TABLE jira_search_snapshots
  ADD COLUMN include_archived_projects BOOLEAN NOT NULL DEFAULT FALSE;
