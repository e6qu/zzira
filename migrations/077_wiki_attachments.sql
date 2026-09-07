CREATE TABLE wiki_attachments (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  page_id BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
  file_id TEXT NOT NULL UNIQUE,
  filename TEXT NOT NULL,
  media_type TEXT NOT NULL,
  comment TEXT NOT NULL DEFAULT '',
  size BIGINT NOT NULL CHECK(size>=0),
  version INTEGER NOT NULL DEFAULT 1 CHECK(version>0),
  status TEXT NOT NULL DEFAULT 'current' CHECK(status IN ('current','archived','trashed')),
  author_id TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(page_id,filename)
);

CREATE TABLE wiki_attachment_versions (
  attachment_id BIGINT NOT NULL REFERENCES wiki_attachments(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  filename TEXT NOT NULL,
  media_type TEXT NOT NULL,
  comment TEXT NOT NULL DEFAULT '',
  size BIGINT NOT NULL CHECK(size>=0),
  blob_ref TEXT NOT NULL,
  author_id TEXT NOT NULL REFERENCES users(id),
  message TEXT NOT NULL DEFAULT '',
  minor_edit BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(attachment_id,version)
);

CREATE INDEX wiki_attachments_page ON wiki_attachments(page_id,status,id);
