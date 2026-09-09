CREATE SEQUENCE IF NOT EXISTS issues_jira_id_seq AS BIGINT START WITH 10000;

ALTER TABLE issues
  ADD COLUMN IF NOT EXISTS jira_id BIGINT DEFAULT nextval('issues_jira_id_seq');

UPDATE issues
SET jira_id = nextval('issues_jira_id_seq')
WHERE jira_id IS NULL;

ALTER TABLE issues
  ALTER COLUMN jira_id SET DEFAULT nextval('issues_jira_id_seq'),
  ALTER COLUMN jira_id SET NOT NULL;

ALTER SEQUENCE issues_jira_id_seq OWNED BY issues.jira_id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_issues_jira_id ON issues (jira_id);

SELECT setval(
  'issues_jira_id_seq',
  GREATEST(COALESCE((SELECT MAX(jira_id) FROM issues), 10000), 10000),
  EXISTS (SELECT 1 FROM issues)
);
