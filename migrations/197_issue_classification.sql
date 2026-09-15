-- A work item's data classification level. New work takes its project's
-- default level, and bulk moves adopt or map classifications as Jira does.
ALTER TABLE issues ADD COLUMN classification_level TEXT;
UPDATE issues i SET classification_level = p.default_classification_level
FROM projects p
WHERE p.id = i.project_id AND p.default_classification_level IS NOT NULL AND i.classification_level IS NULL;
