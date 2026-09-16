ALTER TABLE project_versions ADD COLUMN move_unfixed_issues_to TEXT REFERENCES project_versions(id) ON DELETE SET NULL;
