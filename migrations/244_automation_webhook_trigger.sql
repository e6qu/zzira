-- Jira Automation's incoming webhook trigger: a rule carries a secret URL
-- token, and a request to it runs the rule. The request body reaches the run
-- as {{webhookData}}, and the run names the work item it was given, if any.
ALTER TABLE automation_rules
  ADD COLUMN webhook_token TEXT,
  ADD COLUMN webhook_secret TEXT;

CREATE UNIQUE INDEX automation_rules_webhook_token
  ON automation_rules (webhook_token) WHERE webhook_token IS NOT NULL;

ALTER TABLE automation_runs
  ADD COLUMN webhook_data JSONB;
