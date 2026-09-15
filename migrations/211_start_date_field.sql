-- Jira Software sites have a Start date field. With Due date it schedules
-- work on a project's timeline.
CREATE OR REPLACE FUNCTION provision_workspace_start_date_field(target TEXT)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM custom_fields WHERE workspace_id = target AND app_installation_id IS NULL AND name = 'Start date'
  ) THEN
    INSERT INTO custom_fields(id, name, type, description, workspace_id)
    VALUES ('customfield_' || nextval('jira_app_custom_field_id'), 'Start date', 'date',
            'Allows the planned start date for a piece of work to be set.', target);
  END IF;
END;
$$;

SELECT provision_workspace_start_date_field(id) FROM workspaces;

CREATE OR REPLACE FUNCTION provision_workspace_start_date_field_after_insert()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  PERFORM provision_workspace_start_date_field(NEW.id);
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_workspace_start_date_field_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_start_date_field_after_insert();
