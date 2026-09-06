CREATE TABLE issue_forms (
  id               TEXT PRIMARY KEY,
  issue_id         TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  form_template_id TEXT NOT NULL,
  name             TEXT NOT NULL,
  internal         BOOLEAN NOT NULL DEFAULT TRUE,
  submitted        BOOLEAN NOT NULL DEFAULT FALSE,
  locked           BOOLEAN NOT NULL DEFAULT FALSE,
  answers          JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_issue_forms_issue ON issue_forms(issue_id, created_at, id);
