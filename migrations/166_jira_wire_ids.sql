-- The ids clients see for statuses, boards, sprints, filters, quick filters,
-- workflows and projects follow Jira's shapes. Stored ids stay internal; each
-- kind gains the id Jira uses, and legacy non-numeric project ids are rewritten
-- to numeric ones everywhere they are referenced.

-- Statuses: Jira's defaults keep their well-known ids.
CREATE SEQUENCE jira_status_id START 10002;
ALTER TABLE statuses ADD COLUMN jira_id BIGINT;
UPDATE statuses SET jira_id = CASE id WHEN 'st_todo' THEN 10000 WHEN 'st_inprogress' THEN 3 WHEN 'st_done' THEN 10001 END
WHERE id IN ('st_todo','st_inprogress','st_done');
UPDATE statuses s SET jira_id = 10001 + o.n
FROM (SELECT id, row_number() OVER (ORDER BY created_at, id) n FROM statuses WHERE jira_id IS NULL) o WHERE o.id=s.id;
SELECT setval('jira_status_id', GREATEST(10002, COALESCE((SELECT max(jira_id) + 1 FROM statuses), 10002)), false);
ALTER TABLE statuses
  ALTER COLUMN jira_id SET DEFAULT nextval('jira_status_id'),
  ALTER COLUMN jira_id SET NOT NULL;
CREATE UNIQUE INDEX statuses_jira_id ON statuses(jira_id);

-- Filters: the built-in "All issues" filter is Jira's system filter -4.
CREATE SEQUENCE jira_filter_id START 10000;
ALTER TABLE filters ADD COLUMN jira_id BIGINT;
UPDATE filters SET jira_id = -4 WHERE id = 'flt_all';
UPDATE filters f SET jira_id = 9999 + o.n
FROM (SELECT id, row_number() OVER (ORDER BY id) n FROM filters WHERE jira_id IS NULL) o WHERE o.id=f.id;
SELECT setval('jira_filter_id', GREATEST(10000, COALESCE((SELECT max(jira_id) + 1 FROM filters), 10000)), false);
ALTER TABLE filters
  ALTER COLUMN jira_id SET DEFAULT nextval('jira_filter_id'),
  ALTER COLUMN jira_id SET NOT NULL;
CREATE UNIQUE INDEX filters_jira_id ON filters(jira_id);

-- Boards: numeric ids, and the id of the filter each board is built on.
CREATE SEQUENCE jira_board_id START 1;
ALTER TABLE boards ADD COLUMN jira_id BIGINT, ADD COLUMN filter_jira_id BIGINT;
UPDATE boards b SET jira_id = o.n
FROM (SELECT id, row_number() OVER (ORDER BY (id = 'brd_default') DESC, id) n FROM boards) o WHERE o.id=b.id;
UPDATE boards SET filter_jira_id = nextval('jira_filter_id');
SELECT setval('jira_board_id', GREATEST(1, COALESCE((SELECT max(jira_id) + 1 FROM boards), 1)), false);
ALTER TABLE boards
  ALTER COLUMN jira_id SET DEFAULT nextval('jira_board_id'),
  ALTER COLUMN jira_id SET NOT NULL,
  ALTER COLUMN filter_jira_id SET DEFAULT nextval('jira_filter_id'),
  ALTER COLUMN filter_jira_id SET NOT NULL;
CREATE UNIQUE INDEX boards_jira_id ON boards(jira_id);
CREATE UNIQUE INDEX boards_filter_jira_id ON boards(filter_jira_id);

-- Quick filters live on their board; each gains a numeric id.
CREATE SEQUENCE jira_quick_filter_id START 1;
UPDATE boards SET quick_filters = (
  SELECT COALESCE(jsonb_agg(element || jsonb_build_object('jiraId', nextval('jira_quick_filter_id')) ORDER BY position), '[]'::jsonb)
  FROM jsonb_array_elements(quick_filters) WITH ORDINALITY AS item(element, position))
WHERE jsonb_typeof(quick_filters) = 'array' AND jsonb_array_length(quick_filters) > 0;

-- Sprints: numeric ids.
CREATE SEQUENCE jira_sprint_id START 1;
ALTER TABLE sprints ADD COLUMN jira_id BIGINT;
UPDATE sprints s SET jira_id = o.n
FROM (SELECT id, row_number() OVER (ORDER BY id) n FROM sprints) o WHERE o.id=s.id;
SELECT setval('jira_sprint_id', GREATEST(1, COALESCE((SELECT max(jira_id) + 1 FROM sprints), 1)), false);
ALTER TABLE sprints
  ALTER COLUMN jira_id SET DEFAULT nextval('jira_sprint_id'),
  ALTER COLUMN jira_id SET NOT NULL;
CREATE UNIQUE INDEX sprints_jira_id ON sprints(jira_id);

-- Workflows: Jira identifies a workflow by a UUID.
ALTER TABLE workflows ADD COLUMN entity_id UUID NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX workflows_entity_id ON workflows(entity_id);

-- Projects: new projects already take numeric ids. Rewrite any older
-- non-numeric id, and let every reference follow it.
DO $$
DECLARE
  fk RECORD;
  project RECORD;
  col RECORD;
  new_id TEXT;
BEGIN
  FOR fk IN
    SELECT c.conname, c.conrelid::regclass::text AS tbl, pg_get_constraintdef(c.oid) AS def
    FROM pg_constraint c WHERE c.contype = 'f' AND c.confrelid = 'projects'::regclass
  LOOP
    IF fk.def NOT LIKE '%ON UPDATE CASCADE%' THEN
      EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', fk.tbl, fk.conname);
      EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I %s ON UPDATE CASCADE', fk.tbl, fk.conname, fk.def);
    END IF;
  END LOOP;

  FOR project IN SELECT id FROM projects WHERE id !~ '^[0-9]{1,17}$' ORDER BY id LOOP
    new_id := nextval('jira_project_id')::text;
    UPDATE projects SET id = new_id WHERE id = project.id;
    UPDATE role_bindings SET scope_id = new_id WHERE scope_type = 'project' AND scope_id = project.id;
    UPDATE deleted_issue_visibility SET project_id = new_id WHERE project_id = project.id;
    FOR col IN
      SELECT table_name, column_name FROM information_schema.columns
      WHERE table_schema = 'public' AND data_type = 'jsonb'
    LOOP
      EXECUTE format('UPDATE %I SET %I = replace(%I::text, %L, %L)::jsonb WHERE strpos(%I::text, %L) > 0',
        col.table_name, col.column_name, col.column_name,
        '"' || project.id || '"', '"' || new_id || '"', col.column_name, '"' || project.id || '"');
    END LOOP;
    UPDATE automation_rules
    SET rule_scope_aris = ARRAY(SELECT replace(scope, 'project/' || project.id, 'project/' || new_id) FROM unnest(rule_scope_aris) AS scope)
    WHERE EXISTS (SELECT 1 FROM unnest(rule_scope_aris) AS scope WHERE strpos(scope, 'project/' || project.id) > 0);
  END LOOP;
END $$;
