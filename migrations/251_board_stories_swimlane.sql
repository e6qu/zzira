-- A board can group by stories now: the work type hierarchy level tells a
-- story from the epic above it, so the grouping Jira offers needs no second
-- parent link. The check is the record of what a board may be set to.
ALTER TABLE boards DROP CONSTRAINT IF EXISTS boards_swimlane_strategy_check;
ALTER TABLE boards
  ADD CONSTRAINT boards_swimlane_strategy_check
  CHECK (swimlane_strategy IN ('none', 'assignee', 'epic', 'stories', 'project', 'query'));
