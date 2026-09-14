-- Jira's project types. Business (work management) projects are created from
-- the business project management template, so the stored type must allow
-- them, and Jira Product Discovery projects alongside.
ALTER TABLE projects DROP CONSTRAINT projects_project_type_key_check;
ALTER TABLE projects ADD CONSTRAINT projects_project_type_key_check
  CHECK (project_type_key IN ('software', 'service_desk', 'business', 'product_discovery'));
