-- Issue deletion must not fail merely because the issue was assigned to a
-- sprint. Replica cleanup already removes the corresponding local membership.
ALTER TABLE sprint_issues
  DROP CONSTRAINT IF EXISTS sprint_issues_issue_id_fkey;

ALTER TABLE sprint_issues
  ADD CONSTRAINT sprint_issues_issue_id_fkey
  FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE;
