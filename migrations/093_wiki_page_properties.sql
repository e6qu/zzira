CREATE TABLE wiki_page_properties (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  page_id    BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
  key        TEXT NOT NULL CHECK(length(key) BETWEEN 1 AND 255),
  value      JSONB NOT NULL,
  version    INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
  author_id  TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(page_id,key)
);

CREATE TABLE wiki_page_property_versions (
  property_id BIGINT NOT NULL REFERENCES wiki_page_properties(id) ON DELETE CASCADE,
  version     INTEGER NOT NULL,
  value       JSONB NOT NULL,
  author_id   TEXT NOT NULL REFERENCES users(id),
  message     TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(property_id,version)
);

CREATE INDEX wiki_page_properties_page ON wiki_page_properties(page_id,key,id);
