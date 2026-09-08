CREATE TABLE wiki_whiteboard_objects (
  id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  whiteboard_id BIGINT NOT NULL REFERENCES wiki_content(id) ON DELETE CASCADE,
  object_type   TEXT NOT NULL CHECK(object_type IN ('sticky','text','shape')),
  title         TEXT NOT NULL DEFAULT '' CHECK(length(title) <= 255),
  body          TEXT NOT NULL DEFAULT '' CHECK(length(body) <= 10000),
  x             INTEGER NOT NULL CHECK(x BETWEEN 0 AND 5000),
  y             INTEGER NOT NULL CHECK(y BETWEEN 0 AND 5000),
  width         INTEGER NOT NULL CHECK(width BETWEEN 80 AND 1200),
  height        INTEGER NOT NULL CHECK(height BETWEEN 60 AND 1200),
  color         TEXT NOT NULL CHECK(color IN ('yellow','blue','green','pink','gray')),
  created_by    TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX wiki_whiteboard_objects_board ON wiki_whiteboard_objects(whiteboard_id,id);

CREATE TABLE wiki_whiteboard_connectors (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  whiteboard_id  BIGINT NOT NULL REFERENCES wiki_content(id) ON DELETE CASCADE,
  from_object_id BIGINT NOT NULL REFERENCES wiki_whiteboard_objects(id) ON DELETE CASCADE,
  to_object_id   BIGINT NOT NULL REFERENCES wiki_whiteboard_objects(id) ON DELETE CASCADE,
  label          TEXT NOT NULL DEFAULT '' CHECK(length(label) <= 255),
  line_style     TEXT NOT NULL DEFAULT 'solid' CHECK(line_style IN ('solid','dashed')),
  created_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK(from_object_id <> to_object_id)
);
CREATE INDEX wiki_whiteboard_connectors_board ON wiki_whiteboard_connectors(whiteboard_id,id);
