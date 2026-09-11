CREATE SEQUENCE jira_custom_field_context_id START WITH 10000;

CREATE TABLE custom_field_contexts (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_custom_field_context_id'),
  workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
  field_id TEXT NOT NULL REFERENCES custom_fields(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  all_projects BOOLEAN NOT NULL DEFAULT TRUE,
  all_issue_types BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX custom_field_contexts_field_name
  ON custom_field_contexts(field_id, lower(name));
CREATE INDEX custom_field_contexts_field ON custom_field_contexts(field_id, id);

CREATE TABLE custom_field_context_projects (
  context_id BIGINT NOT NULL REFERENCES custom_field_contexts(id) ON DELETE CASCADE,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  PRIMARY KEY (context_id, project_id)
);
CREATE INDEX custom_field_context_projects_project ON custom_field_context_projects(project_id);

CREATE TABLE custom_field_context_issue_types (
  context_id BIGINT NOT NULL REFERENCES custom_field_contexts(id) ON DELETE CASCADE,
  issue_type_id TEXT NOT NULL,
  PRIMARY KEY (context_id, issue_type_id)
);

CREATE TABLE custom_field_context_defaults (
  context_id BIGINT PRIMARY KEY REFERENCES custom_field_contexts(id) ON DELETE CASCADE,
  value JSONB NOT NULL
);

-- Carry the primitive field_contexts rows into the richer model: a field with
-- project rows becomes a project-scoped context, everything else global.
INSERT INTO custom_field_contexts(workspace_id, field_id, name, description, all_projects)
SELECT cf.workspace_id, cf.id,
       CASE WHEN EXISTS(SELECT 1 FROM field_contexts fc WHERE fc.field_id=cf.id)
            THEN 'Project context' ELSE 'Default Configuration Scheme' END,
       'Carried over when custom field contexts were introduced.',
       NOT EXISTS(SELECT 1 FROM field_contexts fc WHERE fc.field_id=cf.id)
FROM custom_fields cf;

INSERT INTO custom_field_context_projects(context_id, project_id)
SELECT c.id, fc.project_id
FROM custom_field_contexts c
JOIN field_contexts fc ON fc.field_id=c.field_id
JOIN projects p ON p.id=fc.project_id
ON CONFLICT DO NOTHING;

DROP TABLE field_contexts;

CREATE OR REPLACE FUNCTION provision_custom_field_context()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO custom_field_contexts(workspace_id,field_id,name,description)
  VALUES(NEW.workspace_id,NEW.id,'Default Configuration Scheme',
         'Applies to every project and work type.');
  RETURN NEW;
END;
$$;
CREATE TRIGGER provision_custom_field_context_after_insert
AFTER INSERT ON custom_fields FOR EACH ROW
EXECUTE FUNCTION provision_custom_field_context();

-- The single answer to "does this custom field apply here, and under which
-- context". A context naming the project or work type outranks a global one.
CREATE OR REPLACE FUNCTION jira_custom_field_context(
  requested_field TEXT,
  requested_project TEXT,
  requested_issue_type TEXT
) RETURNS BIGINT LANGUAGE sql STABLE AS $$
  SELECT c.id FROM custom_field_contexts c
  WHERE c.field_id = requested_field
    AND (c.all_projects OR EXISTS(
      SELECT 1 FROM custom_field_context_projects p
      WHERE p.context_id=c.id AND p.project_id=requested_project))
    AND (requested_issue_type IS NULL OR requested_issue_type='' OR c.all_issue_types OR EXISTS(
      SELECT 1 FROM custom_field_context_issue_types t
      WHERE t.context_id=c.id AND t.issue_type_id=requested_issue_type))
  ORDER BY (NOT c.all_projects) DESC, (NOT c.all_issue_types) DESC, c.id
  LIMIT 1;
$$;
