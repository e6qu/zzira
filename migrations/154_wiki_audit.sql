-- Confluence's audit log is the site's own record of what administrators did.
-- It is not the organization audit log: that one belongs to the organization
-- across its products, and carries a different record.
CREATE TABLE wiki_audit_records (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  author_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  author_name TEXT NOT NULL DEFAULT '',
  remote_address TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  summary TEXT NOT NULL CHECK (length(summary) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL DEFAULT '',
  sys_admin BOOLEAN NOT NULL DEFAULT FALSE,
  super_admin BOOLEAN NOT NULL DEFAULT FALSE,
  affected_object JSONB,
  changed_values JSONB NOT NULL DEFAULT '[]'::jsonb,
  associated_objects JSONB NOT NULL DEFAULT '[]'::jsonb
);
CREATE INDEX wiki_audit_records_lookup
  ON wiki_audit_records(workspace_id, created_at DESC, id DESC);

-- How long a record is kept, from its creation date.
ALTER TABLE wiki_site_settings ADD COLUMN audit_retention_number INTEGER NOT NULL DEFAULT 3;
ALTER TABLE wiki_site_settings ADD COLUMN audit_retention_units TEXT NOT NULL DEFAULT 'MONTHS';
