-- Every list of work items asked the whole project-permission question once
-- per work item, and the function cannot be inlined, so a dashboard over six
-- thousand work items spent most of a second (several on the deployed host)
-- asking about the same dozen projects again and again. Nearly every grant
-- names no work item: a site administrator, anyone, a portal customer, a user,
-- a group, a project role, the project lead or an application role holds the
-- permission on the whole project. Only the assignee, reporter and custom-field
-- holders depend on which work item is asked about.
--
-- The question is split along that line. jira_project_permission_without_issue
-- answers the grants that name no work item, which a list asks once per
-- project; jira_project_permission_by_issue answers the four holders that read
-- the work item, which a list asks only of the work items in projects the
-- first did not admit. jira_has_project_permission is the two together, so
-- the rules live in one place, and its answer is unchanged.

CREATE OR REPLACE FUNCTION jira_project_permission_without_issue(
  requested_workspace TEXT,
  requested_project TEXT,
  requested_user TEXT,
  requested_permission TEXT,
  include_archived BOOLEAN DEFAULT FALSE
) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
WITH project_context AS (
  SELECT p.* FROM projects p
  WHERE p.workspace_id=requested_workspace
    AND (p.lifecycle_state='ACTIVE' OR (include_archived AND p.lifecycle_state='ARCHIVED'))
    AND (p.id=requested_project OR upper(p.key)=upper(requested_project))
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
    OR (pg.holder_type='sd.customer.portal.only' AND requested_user IS NOT NULL AND EXISTS(
      SELECT 1 FROM service_desks sd JOIN service_customers customer
        ON customer.workspace_id=sd.workspace_id AND customer.user_id=requested_user AND customer.active
      JOIN users u ON u.id=requested_user AND u.active
      WHERE sd.project_id=project.id AND (sd.customer_access_open OR EXISTS(
        SELECT 1 FROM service_desk_customers desk_customer
        WHERE desk_customer.service_desk_id=sd.id AND desk_customer.user_id=requested_user AND desk_customer.active
      ) OR EXISTS(
        SELECT 1 FROM service_desk_organizations link
        JOIN service_organization_users member ON member.organization_id=link.organization_id
        WHERE link.service_desk_id=sd.id AND member.user_id=requested_user
      ))
    ))
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
    ))
);
$$;

-- Without a work item, the assignee and reporter holders ask whether the user
-- is the assignee or reporter of any work item in the project, as they always
-- have.
CREATE OR REPLACE FUNCTION jira_project_permission_by_issue(
  requested_workspace TEXT,
  requested_project TEXT,
  requested_user TEXT,
  requested_issue TEXT,
  requested_permission TEXT,
  include_archived BOOLEAN DEFAULT FALSE
) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
WITH project_context AS (
  SELECT p.* FROM projects p
  WHERE p.workspace_id=requested_workspace
    AND (p.lifecycle_state='ACTIVE' OR (include_archived AND p.lifecycle_state='ARCHIVED'))
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
    AND pg.holder_type IN ('assignee','reporter','userCustomField','groupCustomField')
)
SELECT requested_user IS NOT NULL AND EXISTS(SELECT 1 FROM actor_member) AND EXISTS(
  SELECT 1 FROM grants pg CROSS JOIN project_context project
  WHERE (pg.holder_type='assignee' AND EXISTS(
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
);
$$;

CREATE OR REPLACE FUNCTION jira_has_project_permission(
  requested_workspace TEXT,
  requested_project TEXT,
  requested_user TEXT,
  requested_issue TEXT,
  requested_permission TEXT,
  include_archived BOOLEAN DEFAULT FALSE
) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
SELECT jira_project_permission_without_issue(
    requested_workspace, requested_project, requested_user, requested_permission, include_archived)
  OR jira_project_permission_by_issue(
    requested_workspace, requested_project, requested_user, requested_issue, requested_permission, include_archived);
$$;
