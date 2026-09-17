-- Work type hierarchy levels. Jira ships three levels — Subtask (-1), Base (0)
-- and Epic (1) — and lets an administrator add named levels above Epic and put
-- work types on them.
CREATE TABLE issue_type_hierarchy_levels (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  level        INTEGER NOT NULL CHECK (level >= -1),
  name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  PRIMARY KEY (workspace_id, level)
);

INSERT INTO issue_type_hierarchy_levels(workspace_id, level, name)
SELECT w.id, l.level, l.name FROM workspaces w CROSS JOIN (VALUES
  (1, 'Epic'), (0, 'Base'), (-1, 'Subtask')
) AS l(level, name);

CREATE OR REPLACE FUNCTION provision_workspace_hierarchy_levels()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO issue_type_hierarchy_levels(workspace_id, level, name)
  VALUES (NEW.id, 1, 'Epic'), (NEW.id, 0, 'Base'), (NEW.id, -1, 'Subtask');
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_workspace_hierarchy_levels_after_insert
AFTER INSERT ON workspaces
FOR EACH ROW EXECUTE FUNCTION provision_workspace_hierarchy_levels();

-- A work type may now sit above Epic. Subtask types stay at -1.
ALTER TABLE issue_types
  DROP CONSTRAINT issue_types_hierarchy_level_check,
  ADD CONSTRAINT issue_types_hierarchy_level_check CHECK (hierarchy_level >= -1);

-- A shared default work type moves level for one site only, like its other
-- site-scoped changes.
ALTER TABLE issue_metadata_overrides ADD COLUMN hierarchy_level INTEGER;
