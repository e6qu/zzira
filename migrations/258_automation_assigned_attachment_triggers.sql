-- Jira starts a rule when a work item is assigned and when an attachment
-- arrives; both are work item events like the ones already here.
ALTER TABLE automation_rules DROP CONSTRAINT automation_rules_event_trigger_check;
ALTER TABLE automation_rules ADD CONSTRAINT automation_rules_event_trigger_check
  CHECK (event_trigger IN ('created', 'transitioned', 'field_changed', 'commented', 'linked', 'assigned', 'attachment_added'));
