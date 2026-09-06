CREATE SEQUENCE jira_service_approval_id START 1;
CREATE TABLE service_request_approvals (
  id               TEXT PRIMARY KEY DEFAULT nextval('jira_service_approval_id')::text,
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  name             TEXT NOT NULL,
  final_decision   TEXT NOT NULL DEFAULT 'pending' CHECK(final_decision IN ('pending','approved','declined')),
  created_by       TEXT NOT NULL REFERENCES users(id),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at     TIMESTAMPTZ
);
CREATE INDEX idx_service_request_approvals_request ON service_request_approvals(request_issue_id,created_at,id);

CREATE TABLE service_request_approvers (
  approval_id      TEXT NOT NULL REFERENCES service_request_approvals(id) ON DELETE CASCADE,
  user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  decision         TEXT NOT NULL DEFAULT 'pending' CHECK(decision IN ('pending','approved','declined')),
  decided_at       TIMESTAMPTZ,
  PRIMARY KEY(approval_id,user_id)
);
CREATE INDEX idx_service_request_approvers_user ON service_request_approvers(user_id,approval_id);

CREATE TABLE service_temporary_attachments (
  id               TEXT PRIMARY KEY,
  workspace_id     TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  service_desk_id  TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  author_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  filename         TEXT NOT NULL,
  mime_type        TEXT NOT NULL,
  size             BIGINT NOT NULL CHECK(size >= 0),
  blob_ref         TEXT NOT NULL UNIQUE,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at       TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours'
);
CREATE INDEX idx_service_temporary_attachments_expiry ON service_temporary_attachments(expires_at);

CREATE TABLE service_request_attachments (
  attachment_id    TEXT PRIMARY KEY REFERENCES attachments(id) ON DELETE CASCADE,
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  comment_id       TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
  public           BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX idx_service_request_attachments_request ON service_request_attachments(request_issue_id,comment_id,attachment_id);
