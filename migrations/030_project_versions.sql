CREATE SEQUENCE jira_version_id START 10000;
CREATE TABLE project_versions (
    id text PRIMARY KEY DEFAULT nextval('jira_version_id')::text,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    description text NOT NULL DEFAULT '',
    start_date date,
    release_date date,
    released boolean NOT NULL DEFAULT false,
    archived boolean NOT NULL DEFAULT false,
    position bigint NOT NULL DEFAULT 0,
    UNIQUE (project_id, name)
);
CREATE INDEX project_versions_order ON project_versions(project_id, position, id);
CREATE INDEX issues_fix_versions ON issues USING gin((fields->'fixVersions') jsonb_path_ops);
CREATE INDEX issues_affected_versions ON issues USING gin((fields->'versions') jsonb_path_ops);
