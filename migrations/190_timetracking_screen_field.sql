-- Time tracking is a system field of the default screen, so work item forms
-- offer an original estimate wherever time tracking is on.
INSERT INTO screen_tab_fields(workspace_id, screen_id, tab_id, field_id, position)
SELECT tab.workspace_id, tab.screen_id, tab.id, 'timetracking',
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
  FROM unnest(ARRAY['summary','description','assignee','priority','labels',
                    'parent','components','fixVersions','versions','security','timetracking'])
    WITH ORDINALITY AS field(field_id, position);
  RETURN NEW;
END;
$$;
