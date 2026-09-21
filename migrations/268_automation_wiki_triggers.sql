-- Rules also run on what happens to the wiki: a page written, changed,
-- commented on or labelled, and a blog post published.
ALTER TABLE automation_rules DROP CONSTRAINT automation_rules_event_trigger_check;
ALTER TABLE automation_rules ADD CONSTRAINT automation_rules_event_trigger_check
  CHECK (event_trigger IN ('created', 'transitioned', 'field_changed', 'commented', 'linked', 'assigned', 'attachment_added',
                           'deleted', 'moved', 'version_created', 'version_updated', 'version_released',
                           'sprint_started', 'sprint_completed',
                           'page_created', 'page_updated', 'page_commented', 'page_labelled', 'blogpost_created'));
