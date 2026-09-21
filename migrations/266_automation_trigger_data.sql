-- An event that happens to something other than a work item -- a version, a
-- sprint, or a work item that has gone -- carries what it happened to, so the
-- rule it starts can name it. Nothing can be read back afterwards: the
-- deleted work item is not there, and a version read later is the version as
-- it is now rather than as it was when the rule started.
ALTER TABLE automation_runs ADD COLUMN trigger_data JSONB;
