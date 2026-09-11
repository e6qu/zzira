CREATE TABLE sprint_properties (
  sprint_id TEXT NOT NULL REFERENCES sprints(id) ON DELETE CASCADE,
  key TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
  value JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (sprint_id, key)
);

-- Sprints had no explicit order, so Jira's swap had nothing to exchange.
-- Seed the position from creation order within each board.
ALTER TABLE sprints ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
UPDATE sprints SET position = ordered.rank FROM (
  SELECT id, (row_number() OVER (PARTITION BY board_id ORDER BY created_at, id)) - 1 AS rank
  FROM sprints
) ordered WHERE sprints.id = ordered.id;
CREATE INDEX sprints_board_position ON sprints(board_id, position, id);
