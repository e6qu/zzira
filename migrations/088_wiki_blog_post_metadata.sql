ALTER TABLE wiki_blog_posts
  ADD COLUMN classification_level TEXT NOT NULL DEFAULT '';

CREATE TABLE wiki_blog_post_properties (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  blog_post_id BIGINT NOT NULL REFERENCES wiki_blog_posts(id) ON DELETE CASCADE,
  key          TEXT NOT NULL CHECK(length(key) BETWEEN 1 AND 255),
  value        JSONB NOT NULL,
  version      INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
  author_id    TEXT NOT NULL REFERENCES users(id),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(blog_post_id,key)
);

CREATE TABLE wiki_blog_post_property_versions (
  property_id BIGINT NOT NULL REFERENCES wiki_blog_post_properties(id) ON DELETE CASCADE,
  version     INTEGER NOT NULL,
  value       JSONB NOT NULL,
  author_id   TEXT NOT NULL REFERENCES users(id),
  message     TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(property_id,version)
);

CREATE TABLE wiki_blog_post_labels (
  blog_post_id BIGINT NOT NULL REFERENCES wiki_blog_posts(id) ON DELETE CASCADE,
  label_id     BIGINT NOT NULL REFERENCES wiki_labels(id) ON DELETE CASCADE,
  author_id    TEXT NOT NULL REFERENCES users(id),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(blog_post_id,label_id)
);

CREATE TABLE wiki_blog_post_likes (
  blog_post_id BIGINT NOT NULL REFERENCES wiki_blog_posts(id) ON DELETE CASCADE,
  user_id      TEXT NOT NULL REFERENCES users(id),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(blog_post_id,user_id)
);

CREATE INDEX wiki_blog_post_properties_post ON wiki_blog_post_properties(blog_post_id,key,id);
CREATE INDEX wiki_blog_post_labels_label ON wiki_blog_post_labels(label_id,blog_post_id);
CREATE INDEX wiki_blog_post_likes_user ON wiki_blog_post_likes(user_id,blog_post_id);
