-- New project IDs follow Jira's numeric identifier contract. Existing IDs
-- remain valid so stored links and client replicas are not invalidated.
CREATE SEQUENCE jira_project_id START 10000;
SELECT setval('jira_project_id', GREATEST(10000,
  COALESCE((SELECT max(id::bigint)+1 FROM projects WHERE id ~ '^[0-9]{1,17}$'),10000)),false);
