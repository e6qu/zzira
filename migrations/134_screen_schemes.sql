CREATE SEQUENCE jira_screen_scheme_id START WITH 10000;
CREATE SEQUENCE jira_issue_type_screen_scheme_id START WITH 10000;

CREATE TABLE screen_schemes (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_screen_scheme_id'),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX screen_schemes_workspace_name ON screen_schemes(workspace_id, lower(name));
CREATE UNIQUE INDEX screen_schemes_workspace_default ON screen_schemes(workspace_id) WHERE is_default;

-- A screen scheme maps a form operation to a screen. Jira requires the
-- "default" operation; create/edit/view are optional and fall back to it.
-- RESTRICT is what makes a screen deletion conflict with a scheme that uses it.
CREATE TABLE screen_scheme_items (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scheme_id BIGINT NOT NULL REFERENCES screen_schemes(id) ON DELETE CASCADE,
  operation TEXT NOT NULL CHECK (operation IN ('default','create','edit','view')),
  screen_id BIGINT NOT NULL REFERENCES screens(id) ON DELETE RESTRICT,
  PRIMARY KEY (scheme_id, operation)
);
CREATE INDEX screen_scheme_items_screen ON screen_scheme_items(screen_id);

CREATE TABLE issue_type_screen_schemes (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_issue_type_screen_scheme_id'),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX issue_type_screen_schemes_workspace_name
  ON issue_type_screen_schemes(workspace_id, lower(name));
CREATE UNIQUE INDEX issue_type_screen_schemes_workspace_default
  ON issue_type_screen_schemes(workspace_id) WHERE is_default;

-- issue_type_id carries Jira's "default" sentinel for the fallback mapping.
CREATE TABLE issue_type_screen_scheme_items (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scheme_id BIGINT NOT NULL REFERENCES issue_type_screen_schemes(id) ON DELETE CASCADE,
  issue_type_id TEXT NOT NULL,
  screen_scheme_id BIGINT NOT NULL REFERENCES screen_schemes(id) ON DELETE RESTRICT,
  PRIMARY KEY (scheme_id, issue_type_id)
);
CREATE INDEX issue_type_screen_scheme_items_screen_scheme
  ON issue_type_screen_scheme_items(screen_scheme_id);

CREATE TABLE project_issue_type_screen_schemes (
  project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scheme_id BIGINT NOT NULL REFERENCES issue_type_screen_schemes(id) ON DELETE RESTRICT
);
CREATE INDEX project_issue_type_screen_schemes_scheme
  ON project_issue_type_screen_schemes(workspace_id, scheme_id);

-- The default screen becomes authoritative for work item forms, so it must
-- carry every system field the forms rendered before this migration.
INSERT INTO screen_tab_fields(workspace_id, screen_id, tab_id, field_id, position)
SELECT tab.workspace_id, tab.screen_id, tab.id, field.field_id,
       (SELECT COALESCE(max(position)+1,0) FROM screen_tab_fields WHERE tab_id=tab.id) + field.offset
FROM screens screen
JOIN LATERAL (
  SELECT id, workspace_id, screen_id FROM screen_tabs
  WHERE screen_id=screen.id ORDER BY position, id LIMIT 1
) tab ON TRUE
CROSS JOIN LATERAL (
  SELECT field_id, (row_number() OVER ())-1 AS offset FROM unnest(ARRAY[
    'parent','components','fixVersions','versions','security'
  ]) field_id
) field
WHERE screen.is_default
ON CONFLICT (screen_id, field_id) DO NOTHING;

INSERT INTO screen_schemes(workspace_id, name, description, is_default)
SELECT id, 'Default Screen Scheme', 'The default work item form layout.', TRUE FROM workspaces;

INSERT INTO screen_scheme_items(workspace_id, scheme_id, operation, screen_id)
SELECT scheme.workspace_id, scheme.id, 'default', screen.id
FROM screen_schemes scheme
JOIN screens screen ON screen.workspace_id=scheme.workspace_id AND screen.is_default
WHERE scheme.is_default;

INSERT INTO issue_type_screen_schemes(workspace_id, name, description, is_default)
SELECT id, 'Default Issue Type Screen Scheme', 'The default work type form mapping.', TRUE FROM workspaces;

INSERT INTO issue_type_screen_scheme_items(workspace_id, scheme_id, issue_type_id, screen_scheme_id)
SELECT scheme.workspace_id, scheme.id, 'default', screen_scheme.id
FROM issue_type_screen_schemes scheme
JOIN screen_schemes screen_scheme
  ON screen_scheme.workspace_id=scheme.workspace_id AND screen_scheme.is_default
WHERE scheme.is_default;

INSERT INTO project_issue_type_screen_schemes(project_id, workspace_id, scheme_id)
SELECT p.id, p.workspace_id, scheme.id
FROM projects p
JOIN issue_type_screen_schemes scheme
  ON scheme.workspace_id=p.workspace_id AND scheme.is_default;

-- 133 seeded a new workspace's default screen with five fields. Now that the
-- default screen drives the work item forms, it must carry every system field
-- those forms render, for new workspaces as well as existing ones.
CREATE OR REPLACE FUNCTION provision_workspace_default_screen()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE screen BIGINT; tab BIGINT;
BEGIN
  INSERT INTO screens(workspace_id,name,description,is_default)
  VALUES(NEW.id,'Default Screen','The default work item screen.',TRUE)
  RETURNING id INTO screen;
  INSERT INTO screen_tabs(workspace_id,screen_id,name,position)
  VALUES(NEW.id,screen,'Field Tab',0) RETURNING id INTO tab;
  INSERT INTO screen_tab_fields(workspace_id,screen_id,tab_id,field_id,position)
  SELECT NEW.id,screen,tab,field_id,position - 1
  FROM unnest(ARRAY['summary','description','assignee','priority','labels',
                    'parent','components','fixVersions','versions','security'])
    WITH ORDINALITY AS field(field_id, position);
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION provision_workspace_screen_schemes()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE screen BIGINT; screen_scheme BIGINT; issue_type_scheme BIGINT;
BEGIN
  SELECT id INTO screen FROM screens WHERE workspace_id=NEW.id AND is_default;
  INSERT INTO screen_schemes(workspace_id,name,description,is_default)
  VALUES(NEW.id,'Default Screen Scheme','The default work item form layout.',TRUE)
  RETURNING id INTO screen_scheme;
  INSERT INTO screen_scheme_items(workspace_id,scheme_id,operation,screen_id)
  VALUES(NEW.id,screen_scheme,'default',screen);
  INSERT INTO issue_type_screen_schemes(workspace_id,name,description,is_default)
  VALUES(NEW.id,'Default Issue Type Screen Scheme','The default work type form mapping.',TRUE)
  RETURNING id INTO issue_type_scheme;
  INSERT INTO issue_type_screen_scheme_items(workspace_id,scheme_id,issue_type_id,screen_scheme_id)
  VALUES(NEW.id,issue_type_scheme,'default',screen_scheme);
  RETURN NEW;
END;
$$;
-- Runs after 133's default-screen trigger because triggers on one event fire in
-- name order, and this name sorts after provision_workspace_default_screen.
CREATE TRIGGER provision_workspace_screen_schemes_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_screen_schemes();

CREATE OR REPLACE FUNCTION assign_project_issue_type_screen_scheme()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO project_issue_type_screen_schemes(project_id,workspace_id,scheme_id)
  SELECT NEW.id,NEW.workspace_id,id FROM issue_type_screen_schemes
  WHERE workspace_id=NEW.workspace_id AND is_default;
  RETURN NEW;
END;
$$;
CREATE TRIGGER assign_project_issue_type_screen_scheme_after_insert
AFTER INSERT ON projects FOR EACH ROW
EXECUTE FUNCTION assign_project_issue_type_screen_scheme();

-- A new custom field joins the default screen so it stays usable on the forms
-- the default scheme drives, mirroring Jira's addToDefault convenience.
CREATE OR REPLACE FUNCTION add_custom_field_to_default_screen()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE tab BIGINT; screen BIGINT;
BEGIN
  SELECT s.id, t.id INTO screen, tab FROM screens s
  JOIN screen_tabs t ON t.screen_id=s.id
  WHERE s.workspace_id=NEW.workspace_id AND s.is_default
  ORDER BY t.position, t.id LIMIT 1;
  IF screen IS NULL THEN
    RETURN NEW;
  END IF;
  INSERT INTO screen_tab_fields(workspace_id,screen_id,tab_id,field_id,position)
  VALUES(NEW.workspace_id,screen,tab,NEW.id,
    COALESCE((SELECT max(position)+1 FROM screen_tab_fields WHERE tab_id=tab),0))
  ON CONFLICT (screen_id, field_id) DO NOTHING;
  RETURN NEW;
END;
$$;
CREATE TRIGGER add_custom_field_to_default_screen_after_insert
AFTER INSERT ON custom_fields FOR EACH ROW
EXECUTE FUNCTION add_custom_field_to_default_screen();

INSERT INTO screen_tab_fields(workspace_id, screen_id, tab_id, field_id, position)
SELECT tab.workspace_id, tab.screen_id, tab.id, field.id,
       (SELECT COALESCE(max(position)+1,0) FROM screen_tab_fields WHERE tab_id=tab.id)
         + (row_number() OVER (PARTITION BY tab.id ORDER BY field.id)) - 1
FROM screens screen
JOIN LATERAL (
  SELECT id, workspace_id, screen_id FROM screen_tabs
  WHERE screen_id=screen.id ORDER BY position, id LIMIT 1
) tab ON TRUE
JOIN custom_fields field ON field.workspace_id=screen.workspace_id
WHERE screen.is_default
ON CONFLICT (screen_id, field_id) DO NOTHING;

SELECT setval('jira_screen_scheme_id', GREATEST(10000, COALESCE((SELECT max(id) FROM screen_schemes), 9999)), TRUE);
SELECT setval('jira_issue_type_screen_scheme_id', GREATEST(10000, COALESCE((SELECT max(id) FROM issue_type_screen_schemes), 9999)), TRUE);
