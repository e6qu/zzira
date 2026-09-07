ALTER TABLE wiki_pages
  ADD COLUMN classification_level TEXT NOT NULL DEFAULT '';

CREATE TABLE wiki_page_likes (
  page_id    BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(page_id,user_id)
);

CREATE INDEX wiki_page_likes_user ON wiki_page_likes(user_id,page_id);

CREATE TABLE wiki_page_redactions (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  page_id    BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
  version    INTEGER NOT NULL CHECK(version > 0),
  section    TEXT NOT NULL CHECK(section IN ('title','body')),
  pointer    TEXT NOT NULL,
  from_index INTEGER NOT NULL CHECK(from_index >= 0),
  to_index   INTEGER NOT NULL CHECK(to_index >= from_index),
  reason     TEXT NOT NULL DEFAULT '',
  actor_id   TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX wiki_page_redactions_page ON wiki_page_redactions(page_id,created_at,id);

CREATE TABLE wiki_page_custom_content (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  page_id    BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
  type       TEXT NOT NULL REFERENCES wiki_custom_content_types(type),
  title      TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  author_id  TEXT NOT NULL REFERENCES users(id),
  version    INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX wiki_page_custom_content_page ON wiki_page_custom_content(page_id,type,id);
