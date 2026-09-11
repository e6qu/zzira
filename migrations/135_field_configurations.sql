CREATE SEQUENCE jira_field_configuration_id START WITH 10000;
CREATE SEQUENCE jira_field_configuration_scheme_id START WITH 10000;

CREATE TABLE field_configurations (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_field_configuration_id'),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX field_configurations_workspace_name
  ON field_configurations(workspace_id, lower(name));
CREATE UNIQUE INDEX field_configurations_workspace_default
  ON field_configurations(workspace_id) WHERE is_default;

-- Only fields that differ from the default (optional, visible, no override)
-- need a row, so an absent item means "optional and visible".
CREATE TABLE field_configuration_items (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  configuration_id BIGINT NOT NULL REFERENCES field_configurations(id) ON DELETE CASCADE,
  field_id TEXT NOT NULL CHECK (length(field_id) BETWEEN 1 AND 255),
  is_required BOOLEAN NOT NULL DEFAULT FALSE,
  is_hidden BOOLEAN NOT NULL DEFAULT FALSE,
  description TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (configuration_id, field_id),
  CHECK (NOT (is_required AND is_hidden))
);
CREATE INDEX field_configuration_items_field
  ON field_configuration_items(workspace_id, field_id);

CREATE TABLE field_configuration_schemes (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_field_configuration_scheme_id'),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX field_configuration_schemes_workspace_name
  ON field_configuration_schemes(workspace_id, lower(name));
CREATE UNIQUE INDEX field_configuration_schemes_workspace_default
  ON field_configuration_schemes(workspace_id) WHERE is_default;

-- issue_type_id carries Jira's "default" sentinel for the fallback mapping.
CREATE TABLE field_configuration_scheme_items (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scheme_id BIGINT NOT NULL REFERENCES field_configuration_schemes(id) ON DELETE CASCADE,
  issue_type_id TEXT NOT NULL,
  configuration_id BIGINT NOT NULL REFERENCES field_configurations(id) ON DELETE RESTRICT,
  PRIMARY KEY (scheme_id, issue_type_id)
);
CREATE INDEX field_configuration_scheme_items_configuration
  ON field_configuration_scheme_items(configuration_id);

CREATE TABLE project_field_configuration_schemes (
  project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scheme_id BIGINT NOT NULL REFERENCES field_configuration_schemes(id) ON DELETE RESTRICT
);
CREATE INDEX project_field_configuration_schemes_scheme
  ON project_field_configuration_schemes(workspace_id, scheme_id);

INSERT INTO field_configurations(workspace_id, name, description, is_default)
SELECT id, 'Default Field Configuration', 'The default work item field behaviour.', TRUE FROM workspaces;

-- Summary is the only field the command path already required, so the default
-- configuration reproduces today's behaviour exactly.
INSERT INTO field_configuration_items(workspace_id, configuration_id, field_id, is_required)
SELECT workspace_id, id, 'summary', TRUE FROM field_configurations WHERE is_default;

INSERT INTO field_configuration_schemes(workspace_id, name, description, is_default)
SELECT id, 'Default Field Configuration Scheme', 'The default work type field mapping.', TRUE FROM workspaces;

INSERT INTO field_configuration_scheme_items(workspace_id, scheme_id, issue_type_id, configuration_id)
SELECT scheme.workspace_id, scheme.id, 'default', configuration.id
FROM field_configuration_schemes scheme
JOIN field_configurations configuration
  ON configuration.workspace_id=scheme.workspace_id AND configuration.is_default
WHERE scheme.is_default;

INSERT INTO project_field_configuration_schemes(project_id, workspace_id, scheme_id)
SELECT p.id, p.workspace_id, scheme.id
FROM projects p
JOIN field_configuration_schemes scheme
  ON scheme.workspace_id=p.workspace_id AND scheme.is_default;

CREATE OR REPLACE FUNCTION provision_workspace_field_configuration()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE configuration BIGINT; scheme BIGINT;
BEGIN
  INSERT INTO field_configurations(workspace_id,name,description,is_default)
  VALUES(NEW.id,'Default Field Configuration','The default work item field behaviour.',TRUE)
  RETURNING id INTO configuration;
  INSERT INTO field_configuration_items(workspace_id,configuration_id,field_id,is_required)
  VALUES(NEW.id,configuration,'summary',TRUE);
  INSERT INTO field_configuration_schemes(workspace_id,name,description,is_default)
  VALUES(NEW.id,'Default Field Configuration Scheme','The default work type field mapping.',TRUE)
  RETURNING id INTO scheme;
  INSERT INTO field_configuration_scheme_items(workspace_id,scheme_id,issue_type_id,configuration_id)
  VALUES(NEW.id,scheme,'default',configuration);
  RETURN NEW;
END;
$$;
CREATE TRIGGER provision_workspace_field_configuration_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_field_configuration();

CREATE OR REPLACE FUNCTION assign_project_field_configuration_scheme()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO project_field_configuration_schemes(project_id,workspace_id,scheme_id)
  SELECT NEW.id,NEW.workspace_id,id FROM field_configuration_schemes
  WHERE workspace_id=NEW.workspace_id AND is_default;
  RETURN NEW;
END;
$$;
CREATE TRIGGER assign_project_field_configuration_scheme_after_insert
AFTER INSERT ON projects FOR EACH ROW
EXECUTE FUNCTION assign_project_field_configuration_scheme();

-- A deleted custom field must not linger in any field configuration.
CREATE OR REPLACE FUNCTION cleanup_deleted_custom_field_screens()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM screen_tab_fields WHERE field_id = OLD.id;
  DELETE FROM field_configuration_items WHERE field_id = OLD.id;
  RETURN OLD;
END;
$$;

SELECT setval('jira_field_configuration_id', GREATEST(10000, COALESCE((SELECT max(id) FROM field_configurations), 9999)), TRUE);
SELECT setval('jira_field_configuration_scheme_id', GREATEST(10000, COALESCE((SELECT max(id) FROM field_configuration_schemes), 9999)), TRUE);
