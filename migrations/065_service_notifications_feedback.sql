CREATE TABLE service_request_subscriptions (
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  subscribed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(request_issue_id,user_id)
);
INSERT INTO service_request_subscriptions(request_issue_id,user_id)
SELECT issue_id,customer_id FROM service_requests ON CONFLICT DO NOTHING;
INSERT INTO service_request_subscriptions(request_issue_id,user_id)
SELECT request_issue_id,user_id FROM service_request_participants ON CONFLICT DO NOTHING;
CREATE INDEX idx_service_request_subscriptions_user ON service_request_subscriptions(user_id,request_issue_id);

CREATE TABLE service_request_feedback (
  request_issue_id TEXT PRIMARY KEY REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  reporter_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type             TEXT NOT NULL DEFAULT 'csat' CHECK(type='csat'),
  rating           INTEGER NOT NULL CHECK(rating BETWEEN 1 AND 5),
  comment          TEXT NOT NULL DEFAULT '',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
