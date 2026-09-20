-- A board's swimlanes were a strategy name and nothing else, because the only
-- strategy that grouped anything was "assignee" and the assignees are already
-- in the work items. Grouping by a named query needs the queries kept with the
-- board, the way its quick filters are.
ALTER TABLE boards ADD COLUMN swimlanes JSONB NOT NULL DEFAULT '[]'::jsonb;

-- The strategy was constrained to the two groupings the board could render.
-- It renders four now, and the database says so too: the check is the record
-- of what a board may be set to, not a leftover of what it could once do.
ALTER TABLE boards DROP CONSTRAINT IF EXISTS boards_swimlane_strategy_check;
ALTER TABLE boards
  ADD CONSTRAINT boards_swimlane_strategy_check
  CHECK (swimlane_strategy IN ('none', 'assignee', 'epic', 'project', 'query'));
