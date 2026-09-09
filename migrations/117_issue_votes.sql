CREATE TABLE issue_votes (
  issue_id  TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (issue_id, user_id)
);

CREATE INDEX issue_votes_user ON issue_votes (user_id, issue_id);
