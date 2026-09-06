CREATE TABLE service_customers (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  active       BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id,user_id)
);

CREATE TABLE service_requests (
  issue_id        TEXT PRIMARY KEY REFERENCES issues(id) ON DELETE CASCADE,
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  request_type_id TEXT NOT NULL REFERENCES service_request_types(id),
  customer_id     TEXT NOT NULL REFERENCES users(id),
  channel         TEXT NOT NULL DEFAULT 'portal',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_service_requests_customer ON service_requests(workspace_id,customer_id,created_at DESC);
CREATE INDEX idx_service_requests_desk ON service_requests(service_desk_id,request_type_id,created_at DESC);

CREATE TABLE service_request_comments (
  comment_id       TEXT PRIMARY KEY REFERENCES comments(id) ON DELETE CASCADE,
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  public           BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX idx_service_request_comments_request ON service_request_comments(request_issue_id,comment_id);
