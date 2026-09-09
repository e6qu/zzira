CREATE TABLE jql_function_precomputations (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  function_key    TEXT NOT NULL,
  function_name   TEXT NOT NULL,
  field           TEXT NOT NULL,
  operator        TEXT NOT NULL,
  arguments       TEXT[] NOT NULL DEFAULT '{}',
  value           TEXT,
  error           TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  used_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (value IS NULL OR error IS NULL),
  UNIQUE(installation_id,function_key,function_name,field,operator,arguments)
);

CREATE INDEX jql_function_precomputations_app_order
  ON jql_function_precomputations(installation_id,function_key,used_at,id);
CREATE INDEX jql_function_precomputations_workspace
  ON jql_function_precomputations(workspace_id,installation_id,id);
