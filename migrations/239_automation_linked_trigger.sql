-- A rule can start when a work item is linked, so the stored trigger name is
-- one more than the four the column allowed.
ALTER TABLE automation_rules DROP CONSTRAINT automation_rules_event_trigger_check;
ALTER TABLE automation_rules ADD CONSTRAINT automation_rules_event_trigger_check
  CHECK (event_trigger IN ('created', 'transitioned', 'field_changed', 'commented', 'linked'));
