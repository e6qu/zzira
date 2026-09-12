-- Confluence has two ways to say who may do what in a space. A role gathers
-- permissions and is assigned to people; a direct grant gives one subject one
-- permission. This product had only roles, so the v1 permission API had nothing
-- to write to. Grants sit alongside role assignments, and the permission check
-- accepts either.
CREATE TABLE wiki_space_permission_grants (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  space_id BIGINT NOT NULL REFERENCES wiki_spaces(id) ON DELETE CASCADE,
  subject_type TEXT NOT NULL CHECK (subject_type IN ('user','group')),
  subject_id TEXT NOT NULL,
  permission TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX wiki_space_permission_grants_unique
  ON wiki_space_permission_grants(space_id, subject_type, subject_id, permission);
CREATE INDEX wiki_space_permission_grants_space
  ON wiki_space_permission_grants(space_id, permission);
