-- An approval a workflow status opens records that status, how many approvals
-- it needs and the transitions to run once it is decided.
ALTER TABLE service_request_approvals
  ADD COLUMN status_id TEXT,
  ADD COLUMN condition_type TEXT CHECK (condition_type IN ('number','percent','numberPerPrincipal')),
  ADD COLUMN condition_value INTEGER,
  ADD COLUMN transition_approved TEXT,
  ADD COLUMN transition_rejected TEXT;
