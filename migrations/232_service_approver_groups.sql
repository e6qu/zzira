-- A status approval whose approvers come from groups remembers each approver's
-- groups, so Jira's numberPerPrincipal condition can count approvals per group.
CREATE TABLE service_request_approver_groups (
  approval_id TEXT NOT NULL REFERENCES service_request_approvals(id) ON DELETE CASCADE,
  user_id     TEXT NOT NULL,
  group_id    TEXT NOT NULL,
  PRIMARY KEY (approval_id, user_id, group_id),
  FOREIGN KEY (approval_id, user_id) REFERENCES service_request_approvers(approval_id, user_id) ON DELETE CASCADE
);
