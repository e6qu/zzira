-- Issue types, priorities and resolutions as Jira models them.
--
-- Each is site-wide in Jira, and each site starts with the same defaults: the
-- issue types Epic, Story, Task, Sub-task and Bug; the priorities Highest,
-- High, Medium, Low and Lowest; and the resolutions Done, Won't Do, Duplicate
-- and Cannot Reproduce. A site's administrators can rename, reorder and delete
-- those defaults and add their own.
--
-- Here many sites share one database, so a default is stored once and shared,
-- and a site's changes to it are kept per site in issue_metadata_overrides —
-- renaming "Medium" in one site never renames it in another. What a site
-- creates belongs to that site. Clients see Jira's numeric ids; the internal
-- ids stay internal.

-- ---- Issue types ----
ALTER TABLE issue_types
  ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
  ADD COLUMN jira_id BIGINT,
  ADD COLUMN description TEXT NOT NULL DEFAULT '',
  ADD COLUMN hierarchy_level INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN avatar_id BIGINT,
  ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE issue_types SET jira_id=10002, description='A small, distinct piece of work.', hierarchy_level=0 WHERE id='it_task';
UPDATE issue_types SET jira_id=10003, description='A small piece of work that''s part of a larger task.', hierarchy_level=-1, subtask=TRUE WHERE id='it_subtask';
INSERT INTO issue_types(id,name,icon,subtask,jira_id,description,hierarchy_level) VALUES
  ('it_epic','Epic','epic',FALSE,10000,'A big user story that needs to be broken down.',1),
  ('it_story','Story','story',FALSE,10001,'Functionality or a feature expressed as a user goal.',0),
  ('it_bug','Bug','bug',FALSE,10004,'A problem or error.',0)
ON CONFLICT (id) DO NOTHING;
CREATE SEQUENCE issue_types_jira_id_seq AS BIGINT START WITH 10100;
UPDATE issue_types SET jira_id=nextval('issue_types_jira_id_seq') WHERE jira_id IS NULL;
ALTER TABLE issue_types
  ALTER COLUMN jira_id SET DEFAULT nextval('issue_types_jira_id_seq'),
  ALTER COLUMN jira_id SET NOT NULL,
  ADD CONSTRAINT issue_types_jira_id_key UNIQUE (jira_id),
  ADD CONSTRAINT issue_types_hierarchy_level_check CHECK (hierarchy_level BETWEEN -1 AND 1),
  ADD CONSTRAINT issue_types_subtask_level_check CHECK (subtask = (hierarchy_level = -1));
ALTER SEQUENCE issue_types_jira_id_seq OWNED BY issue_types.jira_id;
CREATE UNIQUE INDEX issue_types_workspace_name ON issue_types (COALESCE(workspace_id,''), lower(name));

-- ---- Priorities ----
ALTER TABLE priorities
  ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
  ADD COLUMN jira_id BIGINT,
  ADD COLUMN description TEXT NOT NULL DEFAULT '',
  ADD COLUMN status_color TEXT NOT NULL DEFAULT '#8a8a8a',
  ADD COLUMN avatar_id BIGINT,
  ADD COLUMN position INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE priorities SET jira_id=3, description='Has the potential to affect progress.', status_color='#ffab00', icon_url='/images/icons/priorities/medium.svg', position=3 WHERE id='pr_medium';
INSERT INTO priorities(id,name,icon_url,jira_id,description,status_color,position) VALUES
  ('pr_highest','Highest','/images/icons/priorities/highest.svg',1,'This problem will block progress.','#d04437',1),
  ('pr_high','High','/images/icons/priorities/high.svg',2,'Serious problem that could block progress.','#f15c75',2),
  ('pr_low','Low','/images/icons/priorities/low.svg',4,'Minor problem or easily worked around.','#707070',4),
  ('pr_lowest','Lowest','/images/icons/priorities/lowest.svg',5,'Trivial problem with little or no impact on progress.','#999999',5)
ON CONFLICT (id) DO NOTHING;
CREATE SEQUENCE priorities_jira_id_seq AS BIGINT START WITH 10000;
UPDATE priorities SET jira_id=nextval('priorities_jira_id_seq') WHERE jira_id IS NULL;
ALTER TABLE priorities
  ALTER COLUMN jira_id SET DEFAULT nextval('priorities_jira_id_seq'),
  ALTER COLUMN jira_id SET NOT NULL,
  ADD CONSTRAINT priorities_jira_id_key UNIQUE (jira_id);
ALTER SEQUENCE priorities_jira_id_seq OWNED BY priorities.jira_id;
CREATE UNIQUE INDEX priorities_workspace_name ON priorities (COALESCE(workspace_id,''), lower(name));

-- ---- Resolutions ----
CREATE SEQUENCE resolutions_jira_id_seq AS BIGINT START WITH 10100;
CREATE TABLE resolutions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
  jira_id BIGINT NOT NULL UNIQUE DEFAULT nextval('resolutions_jira_id_seq'),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
  description TEXT NOT NULL DEFAULT '',
  position INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER SEQUENCE resolutions_jira_id_seq OWNED BY resolutions.jira_id;
CREATE UNIQUE INDEX resolutions_workspace_name ON resolutions (COALESCE(workspace_id,''), lower(name));
INSERT INTO resolutions(id,name,description,jira_id,position) VALUES
  ('res_done','Done','Work has been completed on this issue.',10000,1),
  ('res_wont_do','Won''t Do','This issue won''t be actioned.',10001,2),
  ('res_duplicate','Duplicate','The problem is a duplicate of an existing issue.',10002,3),
  ('res_cannot_reproduce','Cannot Reproduce','All attempts at reproducing this issue failed, or not enough information was available to reproduce the issue.',10003,4);

-- ---- A site's changes to a shared default ----
CREATE TABLE issue_metadata_overrides (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  entity_type TEXT NOT NULL CHECK (entity_type IN ('issuetype','priority','resolution')),
  entity_id TEXT NOT NULL,
  name TEXT,
  description TEXT,
  status_color TEXT,
  icon_url TEXT,
  avatar_id BIGINT,
  position INTEGER,
  deleted BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY (workspace_id, entity_type, entity_id)
);

-- ---- Each site's defaults ----
CREATE TABLE workspace_issue_defaults (
  workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  default_priority_id TEXT REFERENCES priorities(id),
  default_resolution_id TEXT REFERENCES resolutions(id)
);

-- ---- Resolution on issues ----
ALTER TABLE issues
  ADD COLUMN resolution_id TEXT REFERENCES resolutions(id),
  ADD COLUMN resolved_at TIMESTAMPTZ;
-- An issue already in a done status was resolved when it got there; the last
-- update is the closest record of that moment.
UPDATE issues i SET resolution_id='res_done', resolved_at=i.updated_at
FROM statuses st WHERE st.id=i.status_id AND st.category='done' AND i.resolution_id IS NULL;
CREATE INDEX issues_resolution ON issues (workspace_id, resolution_id);

-- ---- Issue type properties ----
CREATE TABLE issue_type_properties (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_type_id TEXT NOT NULL REFERENCES issue_types(id) ON DELETE CASCADE,
  key TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
  value JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, issue_type_id, key)
);

-- ---- Issue type schemes ----
CREATE TABLE issue_type_schemes (
  id BIGINT GENERATED BY DEFAULT AS IDENTITY (START WITH 10000) PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  default_issue_type_id TEXT REFERENCES issue_types(id),
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX issue_type_schemes_name ON issue_type_schemes (workspace_id, lower(name));
CREATE UNIQUE INDEX issue_type_schemes_one_default ON issue_type_schemes (workspace_id) WHERE is_default;
CREATE TABLE issue_type_scheme_items (
  scheme_id BIGINT NOT NULL REFERENCES issue_type_schemes(id) ON DELETE CASCADE,
  issue_type_id TEXT NOT NULL REFERENCES issue_types(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  PRIMARY KEY (scheme_id, issue_type_id)
);
CREATE TABLE project_issue_type_schemes (
  project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scheme_id BIGINT NOT NULL REFERENCES issue_type_schemes(id)
);

-- ---- Priority schemes ----
CREATE TABLE priority_schemes (
  id BIGINT GENERATED BY DEFAULT AS IDENTITY (START WITH 10000) PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  default_priority_id TEXT NOT NULL REFERENCES priorities(id),
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX priority_schemes_name ON priority_schemes (workspace_id, lower(name));
CREATE UNIQUE INDEX priority_schemes_one_default ON priority_schemes (workspace_id) WHERE is_default;
CREATE TABLE priority_scheme_items (
  scheme_id BIGINT NOT NULL REFERENCES priority_schemes(id) ON DELETE CASCADE,
  priority_id TEXT NOT NULL REFERENCES priorities(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  PRIMARY KEY (scheme_id, priority_id)
);
CREATE TABLE project_priority_schemes (
  project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  scheme_id BIGINT NOT NULL REFERENCES priority_schemes(id)
);

-- ---- Provisioning: every site starts with Jira's defaults ----
CREATE OR REPLACE FUNCTION provision_workspace_issue_metadata(target TEXT)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE type_scheme BIGINT; priority_scheme BIGINT;
BEGIN
  INSERT INTO workspace_issue_defaults(workspace_id,default_priority_id,default_resolution_id)
  VALUES(target,'pr_medium','res_done') ON CONFLICT (workspace_id) DO NOTHING;

  IF NOT EXISTS (SELECT 1 FROM issue_type_schemes WHERE workspace_id=target AND is_default) THEN
    INSERT INTO issue_type_schemes(workspace_id,name,description,default_issue_type_id,is_default)
    VALUES(target,'Default Issue Type Scheme','Default issue type scheme is the list of global issue types. All newly created issue types will automatically be added to this scheme.','it_task',TRUE)
    RETURNING id INTO type_scheme;
    INSERT INTO issue_type_scheme_items(scheme_id,issue_type_id,position)
    SELECT type_scheme, t.id, row_number() OVER (ORDER BY t.jira_id) - 1
    FROM issue_types t WHERE t.workspace_id IS NULL;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM priority_schemes WHERE workspace_id=target AND is_default) THEN
    INSERT INTO priority_schemes(workspace_id,name,description,default_priority_id,is_default)
    VALUES(target,'Default priority scheme','This is the default priority scheme used by all projects without any other scheme assigned.','pr_medium',TRUE)
    RETURNING id INTO priority_scheme;
    INSERT INTO priority_scheme_items(scheme_id,priority_id,position)
    SELECT priority_scheme, p.id, p.position FROM priorities p WHERE p.workspace_id IS NULL;
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION provision_workspace_issue_metadata_after_insert()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  PERFORM provision_workspace_issue_metadata(NEW.id);
  RETURN NEW;
END;
$$;
CREATE TRIGGER provision_workspace_issue_metadata_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_issue_metadata_after_insert();

SELECT provision_workspace_issue_metadata(id) FROM workspaces;
