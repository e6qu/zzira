CREATE SEQUENCE jira_notification_scheme_id START 10001;
CREATE SEQUENCE jira_event_notification_id START 10000;

CREATE TABLE notification_schemes (
  id           BIGINT NOT NULL DEFAULT nextval('jira_notification_scheme_id'),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  is_default   BOOLEAN NOT NULL DEFAULT false,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id,id)
);
CREATE UNIQUE INDEX notification_schemes_workspace_name
  ON notification_schemes(workspace_id,lower(name));
CREATE UNIQUE INDEX notification_schemes_one_default
  ON notification_schemes(workspace_id) WHERE is_default;

CREATE TABLE notification_scheme_entries (
  id                BIGINT PRIMARY KEY DEFAULT nextval('jira_event_notification_id'),
  workspace_id      TEXT NOT NULL,
  scheme_id         BIGINT NOT NULL,
  event_id          BIGINT NOT NULL CHECK (event_id > 0),
  notification_type TEXT NOT NULL CHECK (notification_type IN (
    'CurrentAssignee','Reporter','CurrentUser','ProjectLead','ComponentLead',
    'User','Group','ProjectRole','EmailAddress','AllWatchers',
    'UserCustomField','GroupCustomField')),
  parameter         TEXT,
  recipient         TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id,scheme_id)
    REFERENCES notification_schemes(workspace_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX notification_scheme_entries_unique
  ON notification_scheme_entries(workspace_id,scheme_id,event_id,notification_type,COALESCE(recipient,''));
CREATE INDEX notification_scheme_entries_event
  ON notification_scheme_entries(workspace_id,scheme_id,event_id,id);

CREATE TABLE project_notification_schemes (
  project_id   TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  scheme_id    BIGINT NOT NULL,
  assigned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id,scheme_id)
    REFERENCES notification_schemes(workspace_id,id)
);
CREATE INDEX project_notification_schemes_scheme
  ON project_notification_schemes(workspace_id,scheme_id);

CREATE TABLE notification_event_deliveries (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  action_seq   BIGINT NOT NULL,
  event_id     BIGINT NOT NULL,
  issue_id     TEXT NOT NULL,
  delivered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id,action_seq,event_id)
);

INSERT INTO notification_schemes(workspace_id,id,name,description,is_default)
SELECT id,nextval('jira_notification_scheme_id'),'Default Notification Scheme',
       'Default notifications for work item activity.',true
FROM workspaces;

INSERT INTO notification_scheme_entries(workspace_id,scheme_id,event_id,notification_type)
SELECT ns.workspace_id,ns.id,event_id,recipient
FROM notification_schemes ns
CROSS JOIN unnest(ARRAY[1,2,3,4,5,6,8,16]::BIGINT[]) event_id
CROSS JOIN unnest(ARRAY['CurrentAssignee','Reporter','AllWatchers']::TEXT[]) recipient
WHERE ns.is_default;

INSERT INTO project_notification_schemes(project_id,workspace_id,scheme_id)
SELECT p.id,p.workspace_id,ns.id
FROM projects p JOIN notification_schemes ns
  ON ns.workspace_id=p.workspace_id AND ns.is_default;

CREATE OR REPLACE FUNCTION provision_workspace_notification_scheme()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE scheme BIGINT;
BEGIN
  INSERT INTO notification_schemes(workspace_id,name,description,is_default)
  VALUES(NEW.id,'Default Notification Scheme','Default notifications for work item activity.',true)
  RETURNING id INTO scheme;
  INSERT INTO notification_scheme_entries(workspace_id,scheme_id,event_id,notification_type)
  SELECT NEW.id,scheme,event_id,recipient
  FROM unnest(ARRAY[1,2,3,4,5,6,8,16]::BIGINT[]) event_id
  CROSS JOIN unnest(ARRAY['CurrentAssignee','Reporter','AllWatchers']::TEXT[]) recipient;
  RETURN NEW;
END;
$$;
CREATE TRIGGER provision_workspace_notification_scheme_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_notification_scheme();

CREATE OR REPLACE FUNCTION assign_project_notification_scheme()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO project_notification_schemes(project_id,workspace_id,scheme_id)
  SELECT NEW.id,NEW.workspace_id,id FROM notification_schemes
  WHERE workspace_id=NEW.workspace_id AND is_default;
  RETURN NEW;
END;
$$;
CREATE TRIGGER assign_project_notification_scheme_after_insert
AFTER INSERT ON projects FOR EACH ROW
EXECUTE FUNCTION assign_project_notification_scheme();
