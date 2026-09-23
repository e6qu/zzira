-- A portal customer could see the portal and not their own request. The
-- default permission scheme granted every work permission to the project's
-- Members role and nothing to the service project customer, so a person who
-- raised a request through the portal -- who holds no seat on the site, which
-- is the whole point of a portal-only customer -- could not browse the work
-- item behind it, comment on it, or attach a file to it. Every check that
-- goes through Jira's project permissions refused them, while the portal
-- carried on offering the comment box and the file input.
--
-- The grant is scoped to a service project by the holder itself: the
-- sd.customer.portal.only holder answers true only where the project has a
-- service desk the person is admitted to, so a software project on the same
-- scheme is unchanged.
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

  INSERT INTO permission_scheme_grants(
    workspace_id,scheme_id,permission_key,holder_type,holder_parameter,holder_value
  )
  SELECT NEW.id,10000,p.permission_key,'sd.customer.portal.only',NULL,NULL
  FROM (VALUES
    ('BROWSE_PROJECTS'),('CREATE_ISSUES'),('ADD_COMMENTS'),
    ('CREATE_ATTACHMENTS'),('DELETE_OWN_ATTACHMENTS')
  ) AS p(permission_key);
  RETURN NEW;
END;
$$;

INSERT INTO permission_scheme_grants(
  workspace_id,scheme_id,permission_key,holder_type,holder_parameter,holder_value
)
SELECT ps.workspace_id,ps.id,p.permission_key,'sd.customer.portal.only',NULL,NULL
FROM permission_schemes ps
CROSS JOIN (VALUES
  ('BROWSE_PROJECTS'),('CREATE_ISSUES'),('ADD_COMMENTS'),
  ('CREATE_ATTACHMENTS'),('DELETE_OWN_ATTACHMENTS')
) AS p(permission_key)
WHERE ps.is_default
ON CONFLICT DO NOTHING;
