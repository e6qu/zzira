-- Migration 086 originally shipped before automated approval idempotency was
-- added to it. Existing databases therefore recorded 086 without this column.
-- Keep migration history immutable and converge those installations here.
ALTER TABLE service_request_approvals
  ADD COLUMN IF NOT EXISTS automation_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS service_request_approval_automation_once
  ON service_request_approvals(request_issue_id,automation_key)
  WHERE automation_key IS NOT NULL;
