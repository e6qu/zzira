-- What counts as an incident is a team's decision: Jira Service Management
-- raises them here, but a team without a service desk marks them with a work
-- type, a label or a field. An empty query keeps the service desk's own
-- incidents, which is what every project has counted so far.
ALTER TABLE project_dora_settings ADD COLUMN incident_jql TEXT NOT NULL DEFAULT '' CHECK (length(incident_jql) <= 2000);
