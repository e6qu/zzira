-- Plans assign work to teams with Jira's Team field, which holds an Atlassian
-- team, so every site has one, as sites with plans do.
CREATE OR REPLACE FUNCTION provision_workspace_team_field(target TEXT)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM custom_fields WHERE workspace_id = target AND app_installation_id IS NULL AND type = 'team'
  ) THEN
    INSERT INTO custom_fields(id, name, type, description, workspace_id)
    VALUES ('customfield_' || nextval('jira_app_custom_field_id'), 'Team', 'team',
      'Associates an Atlassian team with a work item.', target);
  END IF;
END;
$$;

SELECT provision_workspace_team_field(id) FROM workspaces;

CREATE OR REPLACE FUNCTION provision_workspace_team_field_after_insert()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  PERFORM provision_workspace_team_field(NEW.id);
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_workspace_team_field_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_team_field_after_insert();
