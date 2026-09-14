-- A service desk administrator can turn off attachments for a service desk,
-- as in Jira Service Management; desks allow them by default.
ALTER TABLE service_desks ADD COLUMN attachments_enabled BOOLEAN NOT NULL DEFAULT TRUE;
