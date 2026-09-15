-- Jira sprints record when they actually started and completed, apart from
-- the planned start and end dates. Sprint reports and the velocity chart
-- measure from these moments. Existing sprints take them from the sprint
-- lifecycle actions already recorded.
ALTER TABLE sprints
  ADD COLUMN activated_at TIMESTAMPTZ,
  ADD COLUMN completed_at TIMESTAMPTZ;

UPDATE sprints s SET activated_at = (
  SELECT min(a.created_at) FROM actions a
  WHERE a.entity_type = 'sprint' AND a.entity_id = s.id AND a.payload->'sprint'->>'state' IN ('active','closed')
) WHERE s.state IN ('active','closed');

UPDATE sprints s SET completed_at = (
  SELECT min(a.created_at) FROM actions a
  WHERE a.entity_type = 'sprint' AND a.entity_id = s.id AND a.payload->'sprint'->>'state' = 'closed'
) WHERE s.state = 'closed';

UPDATE sprints SET activated_at = COALESCE(start_date, created_at) WHERE state IN ('active','closed') AND activated_at IS NULL;
UPDATE sprints SET completed_at = COALESCE(end_date, activated_at) WHERE state = 'closed' AND completed_at IS NULL;
