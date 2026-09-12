-- A relation is a named, one-way link between two entities: a person
-- favouriting a page, one page named a sibling of another. Confluence supports
-- 'favourite' by default and lets a client name any other relation it needs.
CREATE TABLE wiki_relations (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  source_type TEXT NOT NULL CHECK (source_type IN ('user','space','content')),
  source_key TEXT NOT NULL,
  -- Only content carries a status and a version, so the other entity types
  -- store the neutral values rather than a null that would escape the key.
  source_status TEXT NOT NULL DEFAULT 'current',
  source_version INTEGER NOT NULL DEFAULT 0,
  target_type TEXT NOT NULL CHECK (target_type IN ('user','space','content')),
  target_key TEXT NOT NULL,
  target_status TEXT NOT NULL DEFAULT 'current',
  target_version INTEGER NOT NULL DEFAULT 0,
  created_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (source_type = 'content' OR (source_status = 'current' AND source_version = 0)),
  CHECK (target_type = 'content' OR (target_status = 'current' AND target_version = 0))
);
-- A relation exists or it does not; naming it twice is the same relation.
CREATE UNIQUE INDEX wiki_relations_unique ON wiki_relations(
  workspace_id, name, source_type, source_key, source_status, source_version,
  target_type, target_key, target_status, target_version);
CREATE INDEX wiki_relations_from
  ON wiki_relations(workspace_id, name, source_type, source_key, target_type, id);
CREATE INDEX wiki_relations_to
  ON wiki_relations(workspace_id, name, target_type, target_key, source_type, id);
