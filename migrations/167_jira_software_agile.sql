-- Jira Software: epics, estimation, boards created from filters, and the
-- DevOps provider modules (operations, security, DevOps components, feature
-- flags and remote links).

-- Epic details. An epic is an issue whose type sits at hierarchy level 1; its
-- children are standard issues whose parent is the epic.
CREATE TABLE issue_epics (
  issue_id     TEXT PRIMARY KEY REFERENCES issues(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT,
  color_key    TEXT
    CHECK (color_key IS NULL OR color_key IN ('color_1','color_2','color_3','color_4','color_5','color_6','color_7',
                         'color_8','color_9','color_10','color_11','color_12','color_13','color_14')),
  done         BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Every site has Jira's "Story point estimate" number field.
CREATE OR REPLACE FUNCTION provision_workspace_story_point_field(target TEXT)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM custom_fields WHERE workspace_id = target AND app_installation_id IS NULL AND name = 'Story point estimate'
  ) THEN
    INSERT INTO custom_fields(id, name, type, description, workspace_id)
    VALUES ('customfield_' || nextval('jira_app_custom_field_id'), 'Story point estimate', 'number',
            'Measurement of complexity and/or size of a requirement.', target);
  END IF;
END;
$$;

SELECT provision_workspace_story_point_field(id) FROM workspaces;

CREATE OR REPLACE FUNCTION provision_workspace_story_point_field_after_insert()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  PERFORM provision_workspace_story_point_field(NEW.id);
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_workspace_story_point_field_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_story_point_field_after_insert();

-- Boards: the estimation field (scrum boards estimate with story points), and
-- the saved filter a board was created from.
ALTER TABLE boards
  ADD COLUMN estimation_field_id TEXT REFERENCES custom_fields(id) ON DELETE SET NULL,
  ADD COLUMN source_filter_id TEXT REFERENCES filters(id) ON DELETE SET NULL,
  ADD COLUMN created_by TEXT,
  ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE boards b SET estimation_field_id = (
  SELECT cf.id FROM custom_fields cf JOIN projects p ON p.workspace_id = cf.workspace_id
  WHERE p.id = b.project_id AND cf.name = 'Story point estimate' AND cf.app_installation_id IS NULL
  ORDER BY cf.id LIMIT 1
) WHERE b.type = 'scrum';

CREATE OR REPLACE FUNCTION boards_default_estimation_field()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.type = 'scrum' AND NEW.estimation_field_id IS NULL THEN
    NEW.estimation_field_id := (
      SELECT cf.id FROM custom_fields cf JOIN projects p ON p.workspace_id = cf.workspace_id
      WHERE p.id = NEW.project_id AND cf.name = 'Story point estimate' AND cf.app_installation_id IS NULL
      ORDER BY cf.id LIMIT 1
    );
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER boards_default_estimation_field
BEFORE INSERT ON boards FOR EACH ROW
EXECUTE FUNCTION boards_default_estimation_field();

-- DevOps provider data. Each module stores its entities by provider id; the
-- stored document is exactly what the provider submitted.
CREATE TABLE software_provider_entities (
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  module          TEXT NOT NULL CHECK (module IN ('operations','security','devopscomponents','featureflags','remotelinks')),
  entity_type     TEXT NOT NULL,
  entity_id       TEXT NOT NULL,
  update_sequence BIGINT NOT NULL,
  properties      JSONB NOT NULL DEFAULT '{}'::jsonb,
  issue_ids       TEXT[] NOT NULL DEFAULT '{}',
  payload         JSONB NOT NULL,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, module, entity_type, entity_id)
);

CREATE INDEX software_provider_entities_issues ON software_provider_entities USING GIN (issue_ids);
CREATE INDEX software_provider_entities_properties ON software_provider_entities USING GIN (properties);

CREATE TABLE software_linked_workspaces (
  workspace_id          TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  module                TEXT NOT NULL CHECK (module IN ('operations','security')),
  provider_workspace_id TEXT NOT NULL,
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, module, provider_workspace_id)
);
