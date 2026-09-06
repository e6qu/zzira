CREATE TABLE service_request_participants (
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(request_issue_id,user_id)
);
CREATE INDEX idx_service_request_participants_user ON service_request_participants(user_id,request_issue_id);
