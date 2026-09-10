CREATE SEQUENCE jira_permission_scheme_id START WITH 10001;
CREATE SEQUENCE jira_permission_grant_id START WITH 10000;

CREATE TABLE permission_schemes (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  id BIGINT NOT NULL DEFAULT nextval('jira_permission_scheme_id'),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255 AND name = btrim(name)),
  description TEXT NOT NULL DEFAULT '',
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id)
);
CREATE UNIQUE INDEX permission_schemes_workspace_name
  ON permission_schemes(workspace_id, lower(name));
CREATE UNIQUE INDEX permission_schemes_one_default
  ON permission_schemes(workspace_id) WHERE is_default;

CREATE TABLE permission_scheme_grants (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_permission_grant_id'),
  workspace_id TEXT NOT NULL,
  scheme_id BIGINT NOT NULL,
  permission_key TEXT NOT NULL CHECK (length(permission_key) BETWEEN 1 AND 255),
  holder_type TEXT NOT NULL CHECK (holder_type IN (
    'anyone','applicationRole','assignee','group','groupCustomField',
    'projectLead','projectRole','reporter','sd.customer.portal.only',
    'user','userCustomField'
  )),
  holder_parameter TEXT,
  holder_value TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, scheme_id)
    REFERENCES permission_schemes(workspace_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX permission_scheme_grants_identity
  ON permission_scheme_grants(
    workspace_id,scheme_id,permission_key,holder_type,
    COALESCE(holder_parameter,''),COALESCE(holder_value,'')
  );

CREATE TABLE project_permission_schemes (
  project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  scheme_id BIGINT NOT NULL,
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, scheme_id)
    REFERENCES permission_schemes(workspace_id, id)
);
CREATE INDEX project_permission_schemes_scheme
  ON project_permission_schemes(workspace_id, scheme_id);

INSERT INTO permission_schemes(workspace_id,id,name,description,is_default)
SELECT id,10000,'Default permission scheme',
       'Grants project members the standard Jira work permissions.',TRUE
FROM workspaces;

INSERT INTO permission_scheme_grants(
  workspace_id,scheme_id,permission_key,holder_type,holder_parameter,holder_value
)
SELECT w.id,10000,p.permission_key,'projectRole','10001','10001'
FROM workspaces w CROSS JOIN (VALUES
  ('BROWSE_PROJECTS'),('MANAGE_SPRINTS_PERMISSION'),('SERVICEDESK_AGENT'),
  ('VIEW_DEV_TOOLS'),('VIEW_READONLY_WORKFLOW'),('ASSIGNABLE_USER'),
  ('ASSIGN_ISSUES'),('CLOSE_ISSUES'),('CREATE_ISSUES'),('DELETE_ISSUES'),
  ('EDIT_ISSUES'),('LINK_ISSUES'),('MODIFY_REPORTER'),('MOVE_ISSUES'),
  ('RESOLVE_ISSUES'),('SCHEDULE_ISSUES'),('SET_ISSUE_SECURITY'),
  ('TRANSITION_ISSUES'),('MANAGE_WATCHERS'),('VIEW_VOTERS_AND_WATCHERS'),
  ('ADD_COMMENTS'),('DELETE_ALL_COMMENTS'),('DELETE_OWN_COMMENTS'),
  ('EDIT_ALL_COMMENTS'),('EDIT_OWN_COMMENTS'),('CREATE_ATTACHMENTS'),
  ('DELETE_ALL_ATTACHMENTS'),('DELETE_OWN_ATTACHMENTS'),('DELETE_ALL_WORKLOGS'),
  ('DELETE_OWN_WORKLOGS'),('EDIT_ALL_WORKLOGS'),('EDIT_OWN_WORKLOGS'),
  ('WORK_ON_ISSUES')
) AS p(permission_key);

INSERT INTO permission_scheme_grants(
  workspace_id,scheme_id,permission_key,holder_type,holder_parameter,holder_value
)
SELECT w.id,10000,p.permission_key,'projectRole','10000','10000'
FROM workspaces w CROSS JOIN (VALUES
  ('ADMINISTER_PROJECTS'),('EDIT_WORKFLOW'),('EDIT_ISSUE_LAYOUT')
) AS p(permission_key);

INSERT INTO project_permission_schemes(project_id,workspace_id,scheme_id)
SELECT p.id,p.workspace_id,10000 FROM projects p;

CREATE OR REPLACE FUNCTION provision_workspace_permission_scheme()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO permission_schemes(workspace_id,id,name,description,is_default)
  VALUES(NEW.id,10000,'Default permission scheme',
         'Grants project members the standard Jira work permissions.',TRUE);

  INSERT INTO permission_scheme_grants(
    workspace_id,scheme_id,permission_key,holder_type,holder_parameter,holder_value
  )
  SELECT NEW.id,10000,p.permission_key,'projectRole','10001','10001'
  FROM (VALUES
    ('BROWSE_PROJECTS'),('MANAGE_SPRINTS_PERMISSION'),('SERVICEDESK_AGENT'),
    ('VIEW_DEV_TOOLS'),('VIEW_READONLY_WORKFLOW'),('ASSIGNABLE_USER'),
    ('ASSIGN_ISSUES'),('CLOSE_ISSUES'),('CREATE_ISSUES'),('DELETE_ISSUES'),
    ('EDIT_ISSUES'),('LINK_ISSUES'),('MODIFY_REPORTER'),('MOVE_ISSUES'),
    ('RESOLVE_ISSUES'),('SCHEDULE_ISSUES'),('SET_ISSUE_SECURITY'),
    ('TRANSITION_ISSUES'),('MANAGE_WATCHERS'),('VIEW_VOTERS_AND_WATCHERS'),
    ('ADD_COMMENTS'),('DELETE_ALL_COMMENTS'),('DELETE_OWN_COMMENTS'),
    ('EDIT_ALL_COMMENTS'),('EDIT_OWN_COMMENTS'),('CREATE_ATTACHMENTS'),
    ('DELETE_ALL_ATTACHMENTS'),('DELETE_OWN_ATTACHMENTS'),('DELETE_ALL_WORKLOGS'),
    ('DELETE_OWN_WORKLOGS'),('EDIT_ALL_WORKLOGS'),('EDIT_OWN_WORKLOGS'),
    ('WORK_ON_ISSUES')
  ) AS p(permission_key);

  INSERT INTO permission_scheme_grants(
    workspace_id,scheme_id,permission_key,holder_type,holder_parameter,holder_value
  )
  SELECT NEW.id,10000,p.permission_key,'projectRole','10000','10000'
  FROM (VALUES
    ('ADMINISTER_PROJECTS'),('EDIT_WORKFLOW'),('EDIT_ISSUE_LAYOUT')
  ) AS p(permission_key);
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_workspace_permission_scheme_after_insert
AFTER INSERT ON workspaces
FOR EACH ROW EXECUTE FUNCTION provision_workspace_permission_scheme();

CREATE OR REPLACE FUNCTION provision_project_permission_scheme()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO project_permission_schemes(project_id,workspace_id,scheme_id)
  SELECT NEW.id,NEW.workspace_id,id FROM permission_schemes
  WHERE workspace_id=NEW.workspace_id AND is_default;
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_project_permission_scheme_after_insert
AFTER INSERT ON projects
FOR EACH ROW EXECUTE FUNCTION provision_project_permission_scheme();

CREATE OR REPLACE FUNCTION cleanup_deleted_user_role_actors()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM project_role_default_actors
  WHERE principal_type='user' AND principal_id=OLD.id;
  DELETE FROM role_bindings
  WHERE principal_type='user' AND principal_id=OLD.id;
  DELETE FROM permission_scheme_grants
  WHERE holder_type='user' AND holder_value=OLD.id;
  RETURN OLD;
END;
$$;

CREATE OR REPLACE FUNCTION cleanup_deleted_group_role_actors()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM project_role_default_actors
  WHERE principal_type='group' AND principal_id=OLD.id::text;
  DELETE FROM role_bindings
  WHERE principal_type='group' AND principal_id=OLD.id::text;
  DELETE FROM permission_scheme_grants
  WHERE holder_type='group' AND holder_value=OLD.id::text;
  RETURN OLD;
END;
$$;

CREATE OR REPLACE FUNCTION sync_permission_grant_group_name()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.name IS DISTINCT FROM NEW.name THEN
    UPDATE permission_scheme_grants
    SET holder_parameter=NEW.name
    WHERE holder_type='group' AND holder_value=NEW.id::text;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER sync_permission_grant_group_name_after_update
AFTER UPDATE OF name ON groups
FOR EACH ROW EXECUTE FUNCTION sync_permission_grant_group_name();

CREATE OR REPLACE FUNCTION jira_has_project_permission(
  requested_workspace TEXT,
  requested_project TEXT,
  requested_user TEXT,
  requested_issue TEXT,
  requested_permission TEXT
) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
WITH project_context AS (
  SELECT p.* FROM projects p
  WHERE p.workspace_id=requested_workspace AND p.lifecycle_state='ACTIVE'
    AND (p.id=requested_project OR upper(p.key)=upper(requested_project))
), issue_context AS (
  SELECT i.* FROM issues i JOIN project_context p ON p.id=i.project_id
  WHERE requested_issue IS NOT NULL
    AND (i.id=requested_issue OR i.key=requested_issue OR i.jira_id::text=requested_issue)
), actor_groups AS (
  SELECT g.id::text AS id,g.name
  FROM group_members gm JOIN groups g ON g.id=gm.group_id
  JOIN directories d ON d.id=g.directory_id
  JOIN sites si ON si.organization_id=d.organization_id
  WHERE si.workspace_id=requested_workspace AND gm.user_id=requested_user AND d.active
), actor_member AS (
  SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id
  WHERE m.workspace_id=requested_workspace AND m.user_id=requested_user AND u.active
), grants AS (
  SELECT pg.* FROM project_context p
  JOIN project_permission_schemes assignment ON assignment.project_id=p.id
  JOIN permission_scheme_grants pg
    ON pg.workspace_id=assignment.workspace_id AND pg.scheme_id=assignment.scheme_id
  WHERE pg.permission_key=requested_permission
)
SELECT EXISTS(
  SELECT 1 FROM role_bindings rb JOIN sites si ON
    (rb.scope_type='site' AND rb.scope_id=si.id::text) OR
    (rb.scope_type='organization' AND rb.scope_id=si.organization_id::text)
  WHERE si.workspace_id=requested_workspace
    AND rb.role_key IN ('atlassian/org-admin','atlassian/site-admin')
    AND ((rb.principal_type='user' AND rb.principal_id=requested_user) OR
      (rb.principal_type='group' AND rb.principal_id IN (SELECT id FROM actor_groups)))
) OR EXISTS(
  SELECT 1 FROM grants pg CROSS JOIN project_context project
  WHERE pg.holder_type='anyone'
    OR (requested_user IS NOT NULL AND EXISTS(SELECT 1 FROM actor_member) AND (
      (pg.holder_type='user' AND pg.holder_value=requested_user)
      OR (pg.holder_type='group' AND pg.holder_value IN (SELECT id FROM actor_groups))
      OR (pg.holder_type='projectLead' AND project.lead_account_id=requested_user)
      OR (pg.holder_type='projectRole' AND EXISTS(
        SELECT 1 FROM role_bindings rb WHERE rb.scope_type='project'
          AND rb.scope_id=project.id AND rb.role_key=pg.holder_value
          AND ((rb.principal_type='user' AND rb.principal_id=requested_user) OR
            (rb.principal_type='group' AND rb.principal_id IN (SELECT id FROM actor_groups)))
      ))
      OR (pg.holder_type='applicationRole' AND EXISTS(
        SELECT 1 FROM products product JOIN sites si ON si.id=product.site_id
        WHERE si.workspace_id=requested_workspace AND product.enabled
          AND product.product_key=pg.holder_value
      ))
      OR (pg.holder_type='assignee' AND EXISTS(
        SELECT 1 FROM issues i WHERE i.project_id=project.id AND i.assignee_id=requested_user
          AND (requested_issue IS NULL OR EXISTS(SELECT 1 FROM issue_context selected WHERE selected.id=i.id))
      ))
      OR (pg.holder_type='reporter' AND EXISTS(
        SELECT 1 FROM issues i WHERE i.project_id=project.id AND i.reporter_id=requested_user
          AND (requested_issue IS NULL OR EXISTS(SELECT 1 FROM issue_context selected WHERE selected.id=i.id))
      ))
      OR (pg.holder_type='userCustomField' AND EXISTS(
        SELECT 1 FROM issues i WHERE i.project_id=project.id
          AND (requested_issue IS NULL OR EXISTS(SELECT 1 FROM issue_context selected WHERE selected.id=i.id))
          AND jsonb_path_exists(i.fields->pg.holder_value, '$.** ? (@ == $target)',
                jsonb_build_object('target',to_jsonb(requested_user)))
      ))
      OR (pg.holder_type='groupCustomField' AND EXISTS(
        SELECT 1 FROM issues i CROSS JOIN actor_groups actor_group
        WHERE i.project_id=project.id
          AND (requested_issue IS NULL OR EXISTS(SELECT 1 FROM issue_context selected WHERE selected.id=i.id))
          AND (jsonb_path_exists(i.fields->pg.holder_value, '$.** ? (@ == $target)',
                 jsonb_build_object('target',to_jsonb(actor_group.id)))
            OR jsonb_path_exists(i.fields->pg.holder_value, '$.** ? (@ == $target)',
                 jsonb_build_object('target',to_jsonb(actor_group.name))))
      ))
      OR (pg.holder_type='sd.customer.portal.only' AND EXISTS(
        SELECT 1 FROM service_desks sd JOIN service_customers customer
          ON customer.workspace_id=sd.workspace_id AND customer.user_id=requested_user AND customer.active
        WHERE sd.project_id=project.id AND (sd.customer_access_open OR EXISTS(
          SELECT 1 FROM service_desk_customers desk_customer
          WHERE desk_customer.service_desk_id=sd.id AND desk_customer.user_id=requested_user AND desk_customer.active
        ))
      ))
    ))
);
$$;
