-- A scheduled automation rule can follow a Quartz cron expression in its
-- timezone instead of a fixed interval.
ALTER TABLE automation_rules
  ADD COLUMN cron_expression TEXT,
  ADD CONSTRAINT automation_rules_one_schedule CHECK (interval_minutes IS NULL OR cron_expression IS NULL);
CREATE INDEX automation_rules_cron_due ON automation_rules (workspace_id, next_run_at)
  WHERE state = 'ENABLED' AND cron_expression IS NOT NULL;
