-- A plan is a sandbox: changes to work items made in a plan are kept in one of
-- its scenarios until someone saves them to Jira. Every plan has a default
-- scenario, whose id is the plan's scenario id; other scenarios start blank
-- or as a copy of another.
CREATE TABLE plan_scenarios (
  id         BIGINT PRIMARY KEY,
  plan_id    BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
  name       TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
  color      TEXT NOT NULL CHECK (color IN ('blue','green','orange','purple','red','teal')),
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (plan_id, name)
);
CREATE INDEX plan_scenarios_plan ON plan_scenarios(plan_id, id);

INSERT INTO plan_scenarios(id, plan_id, name, color, created_by)
SELECT scenario_id, id, 'Default', 'blue', created_by FROM plans;

CREATE FUNCTION create_default_plan_scenario() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO plan_scenarios(id, plan_id, name, color, created_by)
  VALUES (NEW.scenario_id, NEW.id, 'Default', 'blue', NEW.created_by);
  RETURN NEW;
END;
$$;
CREATE TRIGGER create_default_plan_scenario AFTER INSERT ON plans
  FOR EACH ROW EXECUTE FUNCTION create_default_plan_scenario();

-- A change to one field of a work item in a scenario. The value is what the
-- field becomes, or null to clear it.
CREATE TABLE plan_scenario_changes (
  scenario_id BIGINT NOT NULL REFERENCES plan_scenarios(id) ON DELETE CASCADE,
  issue_id    TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  field       TEXT NOT NULL CHECK (field IN ('summary','startDate','endDate','team','sprint','estimate')),
  value       JSONB NOT NULL,
  changed_by  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  changed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (scenario_id, issue_id, field)
);

-- The capacity a planner gives one iteration of a team in a scenario: a
-- sprint by its id, or a Kanban week by the day it starts.
CREATE TABLE plan_scenario_iteration_capacity (
  scenario_id BIGINT NOT NULL REFERENCES plan_scenarios(id) ON DELETE CASCADE,
  team_id     BIGINT NOT NULL REFERENCES plan_teams(id) ON DELETE CASCADE,
  iteration   TEXT NOT NULL CHECK (char_length(iteration) BETWEEN 1 AND 64),
  capacity    DOUBLE PRECISION NOT NULL CHECK (capacity >= 0),
  changed_by  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  changed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (scenario_id, team_id, iteration)
);
