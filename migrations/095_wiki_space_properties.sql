CREATE TABLE wiki_space_properties (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  space_id   BIGINT NOT NULL REFERENCES wiki_spaces(id) ON DELETE CASCADE,
  key        TEXT NOT NULL CHECK(length(key) BETWEEN 1 AND 255),
  value      JSONB NOT NULL,
  version    INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
  author_id  TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(space_id,key)
);

CREATE TABLE wiki_space_property_versions (
  property_id BIGINT NOT NULL REFERENCES wiki_space_properties(id) ON DELETE CASCADE,
  version     INTEGER NOT NULL,
  value       JSONB NOT NULL,
  author_id   TEXT NOT NULL REFERENCES users(id),
  message     TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(property_id,version)
);

CREATE INDEX wiki_space_properties_space ON wiki_space_properties(space_id,key,id);
