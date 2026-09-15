-- Everyone editing a live document sees where the others are working. Each
-- editor's caret or selection is kept in the document's own text and moves
-- with every change accepted after it; restarting the document forgets them.
CREATE TABLE wiki_live_cursors (
  content_type  TEXT NOT NULL,
  content_id    BIGINT NOT NULL,
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  position      INTEGER NOT NULL CHECK (position >= 0),
  selection_end INTEGER NOT NULL CHECK (selection_end >= position),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (content_type, content_id, user_id),
  FOREIGN KEY (content_type, content_id)
    REFERENCES wiki_live_documents(content_type, content_id) ON DELETE CASCADE
);
