CREATE TABLE wiki_database_columns (
  id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  database_id BIGINT NOT NULL REFERENCES wiki_content(id) ON DELETE CASCADE,
  column_key  TEXT NOT NULL CHECK(length(column_key) BETWEEN 1 AND 64),
  name        TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 255),
  field_type  TEXT NOT NULL CHECK(field_type IN ('text','number','date','checkbox','select')),
  options     JSONB NOT NULL DEFAULT '[]',
  position    INTEGER NOT NULL DEFAULT 0 CHECK(position >= 0),
  UNIQUE(database_id,column_key)
);

CREATE TABLE wiki_database_rows (
  id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  database_id BIGINT NOT NULL REFERENCES wiki_content(id) ON DELETE CASCADE,
  values      JSONB NOT NULL DEFAULT '{}',
  created_by  TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX wiki_database_rows_database ON wiki_database_rows(database_id,id);

CREATE TABLE wiki_database_views (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  database_id    BIGINT NOT NULL REFERENCES wiki_content(id) ON DELETE CASCADE,
  name           TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 255),
  sort_key       TEXT NOT NULL DEFAULT '',
  sort_direction TEXT NOT NULL DEFAULT 'asc' CHECK(sort_direction IN ('asc','desc')),
  filter_key     TEXT NOT NULL DEFAULT '',
  filter_value   TEXT NOT NULL DEFAULT '',
  created_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(database_id,name)
);
