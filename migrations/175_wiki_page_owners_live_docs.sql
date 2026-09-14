-- A page has an owner as well as an author: ownership can be handed to
-- someone else, and the page remembers who held it before. Pages start owned
-- by whoever wrote them.
ALTER TABLE wiki_pages ADD COLUMN owner_id TEXT REFERENCES users(id);
ALTER TABLE wiki_pages ADD COLUMN last_owner_id TEXT REFERENCES users(id);
UPDATE wiki_pages SET owner_id = author_id WHERE owner_id IS NULL;

-- A live doc is a page that is always published: it has no drafts, and every
-- edit is the current version.
ALTER TABLE wiki_pages ADD COLUMN subtype TEXT NOT NULL DEFAULT '' CHECK (subtype IN ('', 'live'));
CREATE INDEX wiki_pages_subtype ON wiki_pages(space_id, subtype) WHERE subtype <> '';

-- Starred pages, which is what a page's "favorited by the current user"
-- reports and what the wiki lists as starred.
CREATE TABLE wiki_favourites (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  page_id BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, page_id)
);
CREATE INDEX wiki_favourites_recent ON wiki_favourites(workspace_id, user_id, created_at DESC);
