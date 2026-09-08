CREATE TABLE workflow_schemes (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  default_workflow_id TEXT NOT NULL REFERENCES workflows(id),
  issue_type_mappings JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(issue_type_mappings)='object'),
  draft_def JSONB,
  version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id,name)
);

INSERT INTO workflow_schemes(id,workspace_id,name,default_workflow_id)
SELECT 'scheme_' || substr(md5(p.workspace_id || ':' || COALESCE(p.workflow_id,'wf_default')),1,24),
       p.workspace_id,
       CASE WHEN COALESCE(p.workflow_id,'wf_default')='wf_default'
         THEN 'Default workflow scheme'
         ELSE 'Migrated ' || w.name || ' ' || substr(md5(COALESCE(p.workflow_id,'wf_default')),1,6)
       END,
       COALESCE(p.workflow_id,'wf_default')
FROM projects p
JOIN workflows w ON w.id=COALESCE(p.workflow_id,'wf_default')
GROUP BY p.workspace_id,COALESCE(p.workflow_id,'wf_default'),w.name
ON CONFLICT DO NOTHING;

ALTER TABLE projects ADD COLUMN IF NOT EXISTS workflow_scheme_id TEXT REFERENCES workflow_schemes(id);

UPDATE projects p SET workflow_scheme_id=
  'scheme_' || substr(md5(p.workspace_id || ':' || COALESCE(p.workflow_id,'wf_default')),1,24)
WHERE workflow_scheme_id IS NULL;

CREATE INDEX workflow_schemes_workspace ON workflow_schemes(workspace_id,name,id);

