-- Plans schedule work on Jira's Target start and Target end fields by
-- default, so every site has both, as sites with plans do.
CREATE OR REPLACE FUNCTION provision_workspace_target_date_fields(target TEXT)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE field RECORD;
BEGIN
  FOR field IN SELECT * FROM (VALUES
    ('Target start', 'The targeted start date. This custom field is created and required by Advanced Roadmaps for Jira.'),
    ('Target end', 'The targeted end date. This custom field is created and required by Advanced Roadmaps for Jira.')
  ) AS f(name, description)
  LOOP
    IF NOT EXISTS (
      SELECT 1 FROM custom_fields WHERE workspace_id = target AND app_installation_id IS NULL AND name = field.name
    ) THEN
      INSERT INTO custom_fields(id, name, type, description, workspace_id)
      VALUES ('customfield_' || nextval('jira_app_custom_field_id'), field.name, 'date', field.description, target);
    END IF;
  END LOOP;
END;
$$;

SELECT provision_workspace_target_date_fields(id) FROM workspaces;

CREATE OR REPLACE FUNCTION provision_workspace_target_date_fields_after_insert()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  PERFORM provision_workspace_target_date_fields(NEW.id);
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_workspace_target_date_fields_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_target_date_fields_after_insert();
