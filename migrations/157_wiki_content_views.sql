-- A view is one person opening one piece of content. Confluence reports how many
-- views content has had and how many distinct people viewed it, each optionally
-- since a date, so every view is kept with who made it and when.
--
-- Pages and blog posts number their ids independently, so page 1 and blog post
-- 1 are different content; the type is part of what a view is of.
CREATE TABLE wiki_content_views (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  content_type TEXT NOT NULL CHECK (content_type IN ('page','blogpost')),
  content_id BIGINT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  viewed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX wiki_content_views_content
  ON wiki_content_views(workspace_id, content_type, content_id, viewed_at);
CREATE INDEX wiki_content_views_viewer
  ON wiki_content_views(workspace_id, content_type, content_id, user_id);
