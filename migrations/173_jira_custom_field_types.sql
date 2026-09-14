-- Jira's custom field types: each field keeps the Jira type key it was created
-- with, and a cascading select's second-level options belong to a parent option.
ALTER TABLE custom_fields ADD COLUMN type_key TEXT NOT NULL DEFAULT '';

UPDATE custom_fields SET type_key = CASE type
  WHEN 'text' THEN 'com.atlassian.jira.plugin.system.customfieldtypes:textfield'
  WHEN 'number' THEN 'com.atlassian.jira.plugin.system.customfieldtypes:float'
  WHEN 'datetime' THEN 'com.atlassian.jira.plugin.system.customfieldtypes:datetime'
  WHEN 'select' THEN 'com.atlassian.jira.plugin.system.customfieldtypes:select'
  WHEN 'multiselect' THEN 'com.atlassian.jira.plugin.system.customfieldtypes:multiselect'
  ELSE '' END
WHERE type_key = '' AND app_installation_id IS NULL;

ALTER TABLE custom_field_options ADD COLUMN parent_id BIGINT REFERENCES custom_field_options(id) ON DELETE CASCADE;

DROP INDEX custom_field_options_context_value;
CREATE UNIQUE INDEX custom_field_options_context_value
  ON custom_field_options(context_id, COALESCE(parent_id, 0), lower(value));
