-- Comments carry content properties in Confluence just as pages and blog posts
-- do: arbitrary JSON under a key, versioned on every change. Footer and inline
-- comments share one table, so they share one property table too.
CREATE TABLE wiki_comment_properties (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  comment_id BIGINT NOT NULL REFERENCES wiki_footer_comments(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value JSONB NOT NULL,
  version INTEGER NOT NULL DEFAULT 1,
  author_id TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (comment_id, key)
);
CREATE INDEX wiki_comment_properties_comment ON wiki_comment_properties(comment_id, key, id);
CREATE TABLE wiki_comment_property_versions (
  property_id BIGINT NOT NULL REFERENCES wiki_comment_properties(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  value JSONB NOT NULL,
  author_id TEXT NOT NULL REFERENCES users(id),
  message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (property_id, version)
);
