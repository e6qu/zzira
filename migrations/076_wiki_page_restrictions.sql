CREATE TABLE wiki_page_restrictions (
  page_id BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
  operation TEXT NOT NULL CHECK (operation IN ('read','update')),
  subject_type TEXT NOT NULL CHECK (subject_type IN ('user','group')),
  subject_id TEXT NOT NULL,
  author_id TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (page_id, operation, subject_type, subject_id)
);

CREATE INDEX wiki_page_restrictions_subject
  ON wiki_page_restrictions (subject_type, subject_id, page_id, operation);
