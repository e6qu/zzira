-- A content state is the label a page carries beyond its text: "Rough draft",
-- "Ready for review". Confluence has two kinds. Space content states are
-- configured per space, and custom ones are made by a writer as they work.
CREATE TABLE wiki_content_states (
  id BIGINT GENERATED ALWAYS AS IDENTITY (START WITH 1000) PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 20),
  color TEXT NOT NULL,
  creator_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- A writer's own custom states are theirs, so the name is unique to them
-- rather than to the workspace.
CREATE UNIQUE INDEX wiki_content_states_owner_name
  ON wiki_content_states(workspace_id, creator_id, lower(name));
CREATE INDEX wiki_content_states_recent
  ON wiki_content_states(workspace_id, creator_id, created_at DESC, id DESC);

-- The state belongs to a version, because setting one publishes a new version
-- without changing the body. The kind is explicit: a space state and a custom
-- state come from different places and could otherwise share an id.
ALTER TABLE wiki_pages ADD COLUMN content_state_id BIGINT;
ALTER TABLE wiki_pages ADD COLUMN content_state_kind TEXT;
ALTER TABLE wiki_pages ADD CONSTRAINT wiki_pages_content_state
  CHECK (num_nonnulls(content_state_id, content_state_kind) <> 1
         AND (content_state_kind IS NULL OR content_state_kind IN ('space','custom')));
ALTER TABLE wiki_page_versions ADD COLUMN content_state_id BIGINT;
ALTER TABLE wiki_page_versions ADD COLUMN content_state_kind TEXT;
ALTER TABLE wiki_page_versions ADD CONSTRAINT wiki_page_versions_content_state
  CHECK (num_nonnulls(content_state_id, content_state_kind) <> 1
         AND (content_state_kind IS NULL OR content_state_kind IN ('space','custom')));
CREATE INDEX wiki_pages_content_state
  ON wiki_pages(space_id, content_state_kind, content_state_id, status);
