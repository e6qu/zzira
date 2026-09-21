-- The events that happen around a work item rather than to it: a deletion,
-- a move between projects, and the versions and sprints work travels in.
ALTER TABLE automation_rules DROP CONSTRAINT automation_rules_event_trigger_check;
ALTER TABLE automation_rules ADD CONSTRAINT automation_rules_event_trigger_check
  CHECK (event_trigger IN ('created', 'transitioned', 'field_changed', 'commented', 'linked', 'assigned', 'attachment_added',
                           'deleted', 'moved', 'version_created', 'version_updated', 'version_released',
                           'sprint_started', 'sprint_completed'));
