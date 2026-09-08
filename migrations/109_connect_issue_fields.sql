ALTER TABLE custom_fields ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;
ALTER TABLE custom_fields ADD COLUMN app_installation_id TEXT REFERENCES app_installations(id) ON DELETE CASCADE;
ALTER TABLE custom_fields ADD COLUMN app_module_key TEXT NOT NULL DEFAULT '';
ALTER TABLE custom_fields ADD COLUMN dynamic BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE custom_fields ADD COLUMN active BOOLEAN NOT NULL DEFAULT true;

ALTER TABLE custom_fields
  ADD CONSTRAINT custom_fields_app_module_unique UNIQUE(app_installation_id, app_module_key);

CREATE INDEX custom_fields_workspace_active_idx ON custom_fields(workspace_id, active, id);

CREATE SEQUENCE jira_app_custom_field_id START WITH 20000;
SELECT setval(
  'jira_app_custom_field_id',
  GREATEST(
    19999,
    COALESCE((SELECT max(substring(id FROM '^customfield_([0-9]+)$')::BIGINT) FROM custom_fields), 19999)
  ),
  true
);
