-- A plan showed one long list of work, in one order, however many hundreds of
-- items its sources held. A view is how a planner reads it: the work grouped
-- by the team, sprint, project, status or assignee it belongs to, narrowed to
-- what they are looking at, and kept under a name so the next person opens
-- the same plan the same way.
CREATE TABLE plan_views (
  id BIGSERIAL PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  plan_id BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120 AND name = btrim(name)),
  group_by TEXT NOT NULL DEFAULT '' CHECK (group_by IN ('','team','sprint','project','status','assignee')),
  query TEXT NOT NULL DEFAULT '' CHECK (length(query) <= 200),
  created_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX plan_views_name ON plan_views(workspace_id, plan_id, lower(name));
