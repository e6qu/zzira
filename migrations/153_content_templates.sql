-- Confluence has two kinds of template. A content template is written by an
-- administrator through the API. A blueprint template comes from a blueprint
-- and cannot be created or updated through the API — but it can be modified
-- for the site or for one space, and a row here is that modification.
CREATE TABLE wiki_content_templates (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  space_id BIGINT REFERENCES wiki_spaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  template_type TEXT NOT NULL DEFAULT 'page' CHECK (template_type IN ('page','blogpost')),
  body TEXT NOT NULL DEFAULT '',
  editor_version TEXT NOT NULL DEFAULT 'v2',
  labels TEXT[] NOT NULL DEFAULT '{}',
  blueprint_module_key TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- A blueprint is modified at most once for the site and once per space.
CREATE UNIQUE INDEX wiki_content_templates_blueprint_site
  ON wiki_content_templates(workspace_id, blueprint_module_key)
  WHERE space_id IS NULL AND blueprint_module_key <> '';
CREATE UNIQUE INDEX wiki_content_templates_blueprint_space
  ON wiki_content_templates(workspace_id, space_id, blueprint_module_key)
  WHERE space_id IS NOT NULL AND blueprint_module_key <> '';
CREATE INDEX wiki_content_templates_scope
  ON wiki_content_templates(workspace_id, space_id, template_type, id);
