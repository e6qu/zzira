CREATE SEQUENCE jira_issue_security_scheme_id START WITH 10000;
CREATE SEQUENCE jira_issue_security_level_id START WITH 10000;
CREATE SEQUENCE jira_issue_security_member_id START WITH 10000;

ALTER TABLE security_schemes
  ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
  ADD COLUMN description TEXT NOT NULL DEFAULT '',
  ADD COLUMN default_level_id TEXT,
  ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE security_schemes scheme
SET workspace_id = source.workspace_id
FROM (
  SELECT security_scheme_id, min(workspace_id) AS workspace_id
  FROM projects WHERE security_scheme_id IS NOT NULL GROUP BY security_scheme_id
) source
WHERE source.security_scheme_id=scheme.id AND scheme.workspace_id IS NULL;

UPDATE security_schemes scheme
SET default_level_id = defaults.id
FROM (
  SELECT s.id AS scheme_id, level->>'id' AS id
  FROM security_schemes s, jsonb_array_elements(s.levels) level
  WHERE COALESCE((level->>'isDefault')::boolean,FALSE)
) defaults
WHERE defaults.scheme_id=scheme.id;

CREATE UNIQUE INDEX security_schemes_workspace_name
  ON security_schemes(workspace_id, lower(name)) WHERE workspace_id IS NOT NULL;
CREATE INDEX security_schemes_workspace ON security_schemes(workspace_id,id);

CREATE TABLE issue_security_level_members (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_issue_security_member_id'),
  workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
  scheme_id TEXT NOT NULL REFERENCES security_schemes(id) ON DELETE CASCADE,
  level_id TEXT NOT NULL,
  holder_type TEXT NOT NULL CHECK (holder_type IN (
    'applicationRole','assignee','group','groupCustomField','projectLead',
    'projectRole','reporter','user','userCustomField'
  )),
  holder_parameter TEXT,
  holder_value TEXT,
  managed BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX issue_security_member_identity
  ON issue_security_level_members(
    scheme_id,level_id,holder_type,
    COALESCE(holder_parameter,''),COALESCE(holder_value,'')
  );
CREATE INDEX issue_security_members_level
  ON issue_security_level_members(workspace_id,scheme_id,level_id,id);

INSERT INTO issue_security_level_members(
  workspace_id,scheme_id,level_id,holder_type,holder_parameter,holder_value
)
SELECT scheme.workspace_id,scheme.id,level->>'id','user',member,member
FROM security_schemes scheme
CROSS JOIN LATERAL jsonb_array_elements(scheme.levels) level
CROSS JOIN LATERAL jsonb_array_elements_text(
  CASE WHEN jsonb_typeof(level->'members')='array' THEN level->'members' ELSE '[]'::jsonb END
) member
ON CONFLICT DO NOTHING;

SELECT setval('jira_issue_security_scheme_id', GREATEST(10000,
  COALESCE((SELECT max(id::bigint) FROM security_schemes WHERE id ~ '^[0-9]+$'),9999)), TRUE);
SELECT setval('jira_issue_security_level_id', GREATEST(10000,
  COALESCE((SELECT max((level->>'id')::bigint) FROM security_schemes,
    jsonb_array_elements(levels) level WHERE level->>'id' ~ '^[0-9]+$'),9999)), TRUE);

CREATE OR REPLACE FUNCTION jira_issue_security_visible(
  requested_workspace TEXT,
  requested_project TEXT,
  requested_issue TEXT,
  requested_user TEXT,
  requested_level TEXT
) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
WITH project_context AS (
  SELECT p.* FROM projects p
  WHERE p.workspace_id=requested_workspace AND p.id=requested_project
), issue_context AS (
  SELECT i.* FROM issues i
  WHERE i.workspace_id=requested_workspace AND i.id=requested_issue
), actor_groups AS (
  SELECT g.id::text AS id,g.name
  FROM group_members gm JOIN groups g ON g.id=gm.group_id
  JOIN directories d ON d.id=g.directory_id
  JOIN sites si ON si.organization_id=d.organization_id
  WHERE si.workspace_id=requested_workspace AND gm.user_id=requested_user AND d.active
), scheme_context AS (
  SELECT s.* FROM project_context p JOIN security_schemes s ON s.id=p.security_scheme_id
), level_context AS (
  SELECT level FROM scheme_context s, jsonb_array_elements(s.levels) level
  WHERE level->>'id'=requested_level
), grants AS (
  SELECT member.* FROM scheme_context scheme
  JOIN issue_security_level_members member ON member.scheme_id=scheme.id
  WHERE member.level_id=requested_level
)
SELECT requested_level IS NULL OR requested_level=''
  OR EXISTS(
    SELECT 1 FROM memberships m
    WHERE m.workspace_id=requested_workspace AND m.user_id=requested_user AND m.role='admin'
  )
  OR EXISTS(
    SELECT 1 FROM role_bindings rb JOIN sites si ON
      (rb.scope_type='site' AND rb.scope_id=si.id::text) OR
      (rb.scope_type='organization' AND rb.scope_id=si.organization_id::text)
    WHERE si.workspace_id=requested_workspace
      AND rb.role_key IN ('atlassian/org-admin','atlassian/site-admin')
      AND ((rb.principal_type='user' AND rb.principal_id=requested_user) OR
        (rb.principal_type='group' AND rb.principal_id IN (SELECT id FROM actor_groups)))
  )
  OR EXISTS(
    SELECT 1 FROM level_context
    WHERE CASE WHEN jsonb_typeof(level->'members')='array'
      THEN level->'members' ELSE '[]'::jsonb END @> jsonb_build_array(requested_user)
  )
  OR EXISTS(
    SELECT 1 FROM grants security_grant CROSS JOIN project_context project
    WHERE (security_grant.holder_type='user' AND security_grant.holder_value=requested_user)
      OR (security_grant.holder_type='group' AND security_grant.holder_value IN (SELECT id FROM actor_groups))
      OR (security_grant.holder_type='projectLead' AND project.lead_account_id=requested_user)
      OR (security_grant.holder_type='projectRole' AND EXISTS(
        SELECT 1 FROM role_bindings rb WHERE rb.scope_type='project'
          AND rb.scope_id=project.id AND rb.role_key=security_grant.holder_value
          AND ((rb.principal_type='user' AND rb.principal_id=requested_user) OR
            (rb.principal_type='group' AND rb.principal_id IN (SELECT id FROM actor_groups)))
      ))
      OR (security_grant.holder_type='applicationRole' AND EXISTS(
        SELECT 1 FROM memberships m JOIN products product ON product.product_key=security_grant.holder_value
        JOIN sites si ON si.id=product.site_id
        WHERE m.workspace_id=requested_workspace AND m.user_id=requested_user
          AND si.workspace_id=requested_workspace AND product.enabled
      ))
      OR (security_grant.holder_type='reporter' AND (
        requested_issue IS NULL OR requested_issue='' OR
        EXISTS(SELECT 1 FROM issue_context issue WHERE issue.reporter_id=requested_user)
      ))
      OR (security_grant.holder_type='assignee' AND EXISTS(
        SELECT 1 FROM issue_context issue WHERE issue.assignee_id=requested_user
      ))
      OR (security_grant.holder_type='userCustomField' AND EXISTS(
        SELECT 1 FROM issue_context issue
        WHERE jsonb_path_exists(issue.fields->security_grant.holder_value, '$.** ? (@ == $target)',
          jsonb_build_object('target',to_jsonb(requested_user)))
      ))
      OR (security_grant.holder_type='groupCustomField' AND EXISTS(
        SELECT 1 FROM issue_context issue CROSS JOIN actor_groups actor_group
        WHERE jsonb_path_exists(issue.fields->security_grant.holder_value, '$.** ? (@ == $target)',
          jsonb_build_object('target',to_jsonb(actor_group.id)))
          OR jsonb_path_exists(issue.fields->security_grant.holder_value, '$.** ? (@ == $target)',
          jsonb_build_object('target',to_jsonb(actor_group.name)))
      ))
  );
$$;

CREATE OR REPLACE FUNCTION cleanup_deleted_user_role_actors()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM project_role_default_actors
  WHERE principal_type='user' AND principal_id=OLD.id;
  DELETE FROM role_bindings
  WHERE principal_type='user' AND principal_id=OLD.id;
  DELETE FROM permission_scheme_grants
  WHERE holder_type='user' AND holder_value=OLD.id;
  DELETE FROM issue_security_level_members
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
  DELETE FROM issue_security_level_members
  WHERE holder_type='group' AND holder_value=OLD.id::text;
  RETURN OLD;
END;
$$;

CREATE OR REPLACE FUNCTION sync_permission_grant_group_name()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.name IS DISTINCT FROM NEW.name THEN
    UPDATE permission_scheme_grants SET holder_parameter=NEW.name
    WHERE holder_type='group' AND holder_value=NEW.id::text;
    UPDATE issue_security_level_members SET holder_parameter=NEW.name
    WHERE holder_type='group' AND holder_value=NEW.id::text;
  END IF;
  RETURN NEW;
END;
$$;
