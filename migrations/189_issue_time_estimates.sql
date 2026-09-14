-- Time tracking: the effort originally estimated for a work item and the
-- effort estimated to remain, in seconds. Time spent is the sum of worklogs.
ALTER TABLE issues
  ADD COLUMN original_estimate_seconds BIGINT CHECK (original_estimate_seconds >= 0),
  ADD COLUMN remaining_estimate_seconds BIGINT CHECK (remaining_estimate_seconds >= 0);
