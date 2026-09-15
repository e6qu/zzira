-- Jira's Due date is a system field. ZZIRA only read it through JQL over the
-- issue's field values, which nothing could set, so it gets its own column.
ALTER TABLE issues ADD COLUMN due_date DATE;

UPDATE issues SET due_date = (fields->>'duedate')::date, fields = fields - 'duedate'
WHERE fields->>'duedate' ~ '^\d{4}-\d{2}-\d{2}$';

CREATE INDEX issues_due_date ON issues (workspace_id, due_date) WHERE due_date IS NOT NULL;

-- Due date is a system field of the default screen, so work item forms offer
-- it as Jira's do.
INSERT INTO screen_tab_fields(workspace_id, screen_id, tab_id, field_id, position)
SELECT tab.workspace_id, tab.screen_id, tab.id, 'duedate',
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
                    'parent','components','fixVersions','versions','security','timetracking','duedate'])
    WITH ORDINALITY AS field(field_id, position);
  RETURN NEW;
END;
$$;
