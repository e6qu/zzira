CREATE SEQUENCE jira_version_related_work_id START WITH 10000;

-- Jira's "related work" is an ordered list of external links an administrator
-- attaches to a release: a category, an optional title, and a URL.
CREATE TABLE version_related_work (
  id TEXT PRIMARY KEY DEFAULT nextval('jira_version_related_work_id')::text,
  version_id TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
  category TEXT NOT NULL CHECK (length(category) BETWEEN 1 AND 255),
  title TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX version_related_work_version ON version_related_work(version_id, id);
