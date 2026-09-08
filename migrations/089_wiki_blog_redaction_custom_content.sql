CREATE TABLE wiki_blog_post_redactions (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  blog_post_id BIGINT NOT NULL REFERENCES wiki_blog_posts(id) ON DELETE CASCADE,
  version      INTEGER NOT NULL CHECK(version > 0),
  section      TEXT NOT NULL CHECK(section IN ('title','body')),
  pointer      TEXT NOT NULL,
  from_index   INTEGER NOT NULL CHECK(from_index >= 0),
  to_index     INTEGER NOT NULL CHECK(to_index >= from_index),
  reason       TEXT NOT NULL DEFAULT '',
  actor_id     TEXT NOT NULL REFERENCES users(id),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX wiki_blog_post_redactions_post ON wiki_blog_post_redactions(blog_post_id,created_at,id);

CREATE TABLE wiki_custom_content_types (
  type                TEXT PRIMARY KEY,
  body_representation TEXT NOT NULL CHECK(body_representation IN ('storage','raw')),
  title               TEXT NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE wiki_blog_custom_content (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  blog_post_id BIGINT NOT NULL REFERENCES wiki_blog_posts(id) ON DELETE CASCADE,
  type         TEXT NOT NULL REFERENCES wiki_custom_content_types(type),
  title        TEXT NOT NULL,
  body         TEXT NOT NULL DEFAULT '',
  author_id    TEXT NOT NULL REFERENCES users(id),
  version      INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX wiki_blog_custom_content_post ON wiki_blog_custom_content(blog_post_id,type,id);

INSERT INTO wiki_custom_content_types(type,body_representation,title) VALUES
  ('com.zzira:diagram','storage','Diagram'),
  ('com.zzira:metric-snapshot','raw','Metric snapshot');
