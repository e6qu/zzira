CREATE TABLE wiki_blog_posts (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  space_id   BIGINT NOT NULL REFERENCES wiki_spaces(id) ON DELETE CASCADE,
  title      TEXT NOT NULL,
  status     TEXT NOT NULL CHECK(status IN ('current','draft','trashed')),
  body       TEXT NOT NULL DEFAULT '',
  author_id  TEXT NOT NULL REFERENCES users(id),
  private    BOOLEAN NOT NULL DEFAULT FALSE,
  published  BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  version    INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX wiki_blog_posts_space ON wiki_blog_posts(space_id,status,id);
CREATE UNIQUE INDEX wiki_blog_post_current_title ON wiki_blog_posts(space_id,title) WHERE status='current';

CREATE TABLE wiki_blog_post_versions (
  blog_post_id BIGINT NOT NULL REFERENCES wiki_blog_posts(id) ON DELETE CASCADE,
  version      INTEGER NOT NULL,
  title        TEXT NOT NULL,
  body         TEXT NOT NULL,
  status       TEXT NOT NULL,
  author_id    TEXT NOT NULL REFERENCES users(id),
  message      TEXT NOT NULL DEFAULT '',
  minor_edit   BOOLEAN NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(blog_post_id,version)
);
