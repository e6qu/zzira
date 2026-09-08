CREATE TABLE wiki_attachment_properties (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  attachment_id BIGINT NOT NULL REFERENCES wiki_attachments(id) ON DELETE CASCADE,
  key TEXT NOT NULL CHECK(length(key) BETWEEN 1 AND 255),
  value JSONB NOT NULL,
  version INTEGER NOT NULL DEFAULT 1 CHECK(version > 0),
  author_id TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(attachment_id,key)
);

CREATE TABLE wiki_attachment_property_versions (
  property_id BIGINT NOT NULL REFERENCES wiki_attachment_properties(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  value JSONB NOT NULL,
  author_id TEXT NOT NULL REFERENCES users(id),
  message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(property_id,version)
);

CREATE TABLE wiki_attachment_labels (
  attachment_id BIGINT NOT NULL REFERENCES wiki_attachments(id) ON DELETE CASCADE,
  label_id BIGINT NOT NULL REFERENCES wiki_labels(id) ON DELETE CASCADE,
  author_id TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(attachment_id,label_id)
);

CREATE INDEX wiki_attachment_properties_attachment ON wiki_attachment_properties(attachment_id,key,id);
CREATE INDEX wiki_attachment_labels_label ON wiki_attachment_labels(label_id,attachment_id);
