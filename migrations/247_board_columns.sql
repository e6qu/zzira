-- A board column is a name and the statuses that stand in it. Until now a
-- column was exactly one status, so it could not be named or group several
-- statuses the way Jira's columns do, and its work-in-progress limit was kept
-- per status in a separate map. Both become one ordered list of columns.
ALTER TABLE boards ADD COLUMN board_columns JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Every existing column keeps its status, its position and its limit, and
-- takes the status's own name -- which is what the board already displayed.
UPDATE boards b SET board_columns = COALESCE((
  SELECT jsonb_agg(
           jsonb_strip_nulls(jsonb_build_object(
             'name', COALESCE(s.name, entry.status_id),
             'statusIds', jsonb_build_array(entry.status_id),
             'limit', NULLIF(COALESCE((b.column_limits->>entry.status_id)::int, 0), 0)
           ))
           ORDER BY entry.ord)
  FROM unnest(b.column_status_ids) WITH ORDINALITY AS entry(status_id, ord)
  LEFT JOIN statuses s ON s.id = entry.status_id
), '[]'::jsonb);

ALTER TABLE boards DROP COLUMN column_status_ids;
ALTER TABLE boards DROP COLUMN column_limits;

-- A new board starts with the same three columns it always started with, one
-- status each, named after the status.
ALTER TABLE boards ALTER COLUMN board_columns SET DEFAULT
  '[{"name":"To Do","statusIds":["st_todo"]},
    {"name":"In Progress","statusIds":["st_inprogress"]},
    {"name":"Done","statusIds":["st_done"]}]'::jsonb;
