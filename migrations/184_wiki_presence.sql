-- Who is looking at, or editing, a page or blog post right now. Presence is
-- refreshed by the open page every few seconds and expires on its own, so it
-- is kept in an unlogged table: nothing in it needs to survive a restart.
CREATE UNLOGGED TABLE wiki_presence (
  workspace_id text NOT NULL,
  content_type text NOT NULL CHECK (content_type IN ('page', 'blogpost')),
  content_id bigint NOT NULL,
  user_id text NOT NULL,
  editing boolean NOT NULL DEFAULT false,
  seen_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (content_type, content_id, user_id)
);
CREATE INDEX wiki_presence_seen ON wiki_presence (seen_at);
