-- Jira's Default Screen carries Environment, so a work item created through
-- the default screen scheme may set it. Ours left it off every default
-- screen, and the create command refuses a field its screen does not offer:
-- POST /rest/api/3/issue with an environment answered 400 on any project
-- that had not customized its screens. Offer it on existing default screens
-- without changing customized ones, and seed it for new sites.
INSERT INTO screen_tab_fields(workspace_id, screen_id, tab_id, field_id, position)
SELECT tab.workspace_id, tab.screen_id, tab.id, 'environment',
       (SELECT COALESCE(max(position)+1,0) FROM screen_tab_fields WHERE tab_id=tab.id)
FROM screens screen
JOIN LATERAL (
  SELECT id, workspace_id, screen_id FROM screen_tabs
  WHERE screen_id=screen.id ORDER BY position, id LIMIT 1
) tab ON TRUE
WHERE screen.is_default
ON CONFLICT (screen_id, field_id) DO NOTHING;

CREATE OR REPLACE FUNCTION provision_workspace_default_screen()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE screen BIGINT; tab BIGINT;
BEGIN
  INSERT INTO screens(workspace_id,name,description,is_default)
  VALUES(NEW.id,'Default Screen','The default work item screen.',TRUE)
  RETURNING id INTO screen;
  INSERT INTO screen_tabs(workspace_id,screen_id,name,position)
  VALUES(NEW.id,screen,'Field Tab',0) RETURNING id INTO tab;
  INSERT INTO screen_tab_fields(workspace_id,screen_id,tab_id,field_id,position)
  SELECT NEW.id,screen,tab,field_id,position - 1
  FROM unnest(ARRAY['summary','description','environment','assignee','reporter','priority','labels',
                    'parent','components','fixVersions','versions','security','timetracking','duedate'])
    WITH ORDINALITY AS field(field_id, position);
  RETURN NEW;
END;
$$;
