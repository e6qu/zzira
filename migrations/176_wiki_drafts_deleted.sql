-- Confluence moves content through three deletions. Deleting sends it to the
-- trash; purging a trashed page or blog post takes it out of the trash into the
-- deleted state, which only space administrators see and from which it can
-- still be restored; and discarding a draft removes the draft for good.
ALTER TABLE wiki_pages DROP CONSTRAINT wiki_pages_status_check;
ALTER TABLE wiki_pages ADD CONSTRAINT wiki_pages_status_check
  CHECK (status IN ('current','draft','trashed','archived','deleted'));
ALTER TABLE wiki_blog_posts DROP CONSTRAINT wiki_blog_posts_status_check;
ALTER TABLE wiki_blog_posts ADD CONSTRAINT wiki_blog_posts_status_check
  CHECK (status IN ('current','draft','trashed','deleted'));

-- A published page or blog post can have one unpublished draft beside it. The
-- draft is shared by everyone who may edit the content, and publishing
-- replaces it.
CREATE TABLE wiki_content_drafts (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  content_type TEXT NOT NULL CHECK (content_type IN ('page','blogpost')),
  content_id BIGINT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  author_id TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (content_type, content_id)
);

-- Blog posts are starred as pages are.
CREATE TABLE wiki_blog_post_favourites (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  blog_post_id BIGINT NOT NULL REFERENCES wiki_blog_posts(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, blog_post_id)
);
CREATE INDEX wiki_blog_post_favourites_recent ON wiki_blog_post_favourites(workspace_id, user_id, created_at DESC);
