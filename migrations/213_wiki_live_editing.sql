-- A published page being edited by several people has one shared live
-- document. Every accepted change is kept in order, so an editor that missed
-- some can rebase its own work on them. Publishing the page closes the
-- session; the session id changes whenever the document restarts, so editors
-- know to reload it.
CREATE TABLE wiki_live_documents (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  page_id BIGINT PRIMARY KEY REFERENCES wiki_pages(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  revision BIGINT NOT NULL DEFAULT 0,
  base_version INTEGER NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE wiki_live_changes (
  page_id BIGINT NOT NULL REFERENCES wiki_live_documents(page_id) ON DELETE CASCADE,
  revision BIGINT NOT NULL,
  author_id TEXT NOT NULL REFERENCES users(id),
  position INTEGER NOT NULL CHECK (position >= 0),
  delete_count INTEGER NOT NULL CHECK (delete_count >= 0),
  insert_text TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (page_id, revision)
);
