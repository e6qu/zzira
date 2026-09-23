-- An object could carry typed attributes, a conversation and a history, and
-- still not the things people keep about it: the photograph of the rack, the
-- signed contract, the export of the licence. Assets keeps files on an
-- object, and so does this.
CREATE TABLE service_asset_object_attachments (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  object_id  UUID NOT NULL REFERENCES service_asset_objects(id) ON DELETE CASCADE,
  author_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  filename   TEXT NOT NULL CHECK(length(btrim(filename)) BETWEEN 1 AND 255),
  media_type TEXT NOT NULL DEFAULT 'application/octet-stream',
  size       BIGINT NOT NULL CHECK(size >= 0),
  blob_ref   TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX service_asset_object_attachments_object
  ON service_asset_object_attachments(object_id, created_at, id);
