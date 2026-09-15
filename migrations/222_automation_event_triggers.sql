-- Automation rules can start from work item events: work created,
-- transitioned, commented or with changed fields. Each action records the rule
-- whose run caused it, so rules only trigger other rules when allowed.
ALTER TABLE actions ADD COLUMN automation_rule_uuid UUID;

ALTER TABLE automation_rules ADD COLUMN event_trigger TEXT
  CHECK (event_trigger IN ('created', 'transitioned', 'field_changed', 'commented'));
CREATE INDEX automation_rules_events ON automation_rules (workspace_id)
  WHERE state = 'ENABLED' AND event_trigger IS NOT NULL;

-- A scheduled run is unique per schedule time and an event run per event.
ALTER TABLE automation_runs
  ADD COLUMN trigger_seq BIGINT,
  ADD COLUMN issue_id TEXT,
  ADD COLUMN initiator_id TEXT,
  DROP CONSTRAINT automation_runs_rule_uuid_scheduled_for_key;
CREATE UNIQUE INDEX automation_runs_schedule ON automation_runs (rule_uuid, scheduled_for) WHERE trigger_seq IS NULL;
CREATE UNIQUE INDEX automation_runs_event ON automation_runs (rule_uuid, trigger_seq) WHERE trigger_seq IS NOT NULL;

-- The last action each workspace's rules have considered.
CREATE TABLE automation_event_cursors (
  workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  last_seq BIGINT NOT NULL
);
