CREATE TABLE issue_properties (
  issue_id   TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  key        TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 255),
  value      JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (issue_id, key)
);
