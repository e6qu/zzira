CREATE SEQUENCE jira_screen_id START WITH 10000;
CREATE SEQUENCE jira_screen_tab_id START WITH 10000;

CREATE TABLE screens (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_screen_id'),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX screens_workspace_name ON screens(workspace_id, lower(name));
CREATE UNIQUE INDEX screens_workspace_default ON screens(workspace_id) WHERE is_default;
CREATE INDEX screens_workspace ON screens(workspace_id, id);

CREATE TABLE screen_tabs (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_screen_tab_id'),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  screen_id BIGINT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  position INTEGER NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX screen_tabs_screen_name ON screen_tabs(screen_id, lower(name));
CREATE INDEX screen_tabs_screen_position ON screen_tabs(workspace_id, screen_id, position, id);

-- Jira allows a field to appear at most once per screen, so the identity index
-- spans the screen rather than the tab: moving a field between tabs is an
-- update, never a second row.
CREATE TABLE screen_tab_fields (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  screen_id BIGINT NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
  tab_id BIGINT NOT NULL REFERENCES screen_tabs(id) ON DELETE CASCADE,
  field_id TEXT NOT NULL CHECK (length(field_id) BETWEEN 1 AND 255),
  position INTEGER NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (screen_id, field_id)
);
CREATE INDEX screen_tab_fields_tab_position ON screen_tab_fields(workspace_id, tab_id, position);
CREATE INDEX screen_tab_fields_field ON screen_tab_fields(workspace_id, field_id);

INSERT INTO screens(workspace_id, name, description, is_default)
SELECT id, 'Default Screen', 'The default work item screen.', TRUE FROM workspaces;

INSERT INTO screen_tabs(workspace_id, screen_id, name, position)
SELECT workspace_id, id, 'Field Tab', 0 FROM screens WHERE is_default;

INSERT INTO screen_tab_fields(workspace_id, screen_id, tab_id, field_id, position)
SELECT tab.workspace_id, tab.screen_id, tab.id, field.field_id, field.position - 1
FROM screen_tabs tab
JOIN screens screen ON screen.id = tab.screen_id AND screen.is_default
CROSS JOIN LATERAL unnest(ARRAY[
  'summary','description','assignee','priority','labels'
]) WITH ORDINALITY AS field(field_id, position);

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
  FROM unnest(ARRAY['summary','description','assignee','priority','labels'])
    WITH ORDINALITY AS field(field_id, position);
  RETURN NEW;
END;
$$;
CREATE TRIGGER provision_workspace_default_screen_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_default_screen();

-- A deleted custom field must not linger on any screen.
CREATE OR REPLACE FUNCTION cleanup_deleted_custom_field_screens()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM screen_tab_fields WHERE field_id = OLD.id;
  RETURN OLD;
END;
$$;
CREATE TRIGGER cleanup_deleted_custom_field_screens_before_delete
BEFORE DELETE ON custom_fields FOR EACH ROW
EXECUTE FUNCTION cleanup_deleted_custom_field_screens();

SELECT setval('jira_screen_id', GREATEST(10000, COALESCE((SELECT max(id) FROM screens), 9999)), TRUE);
SELECT setval('jira_screen_tab_id', GREATEST(10000, COALESCE((SELECT max(id) FROM screen_tabs), 9999)), TRUE);
