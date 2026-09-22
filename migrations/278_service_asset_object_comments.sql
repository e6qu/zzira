-- An object could be described and connected and linked to a request, and
-- nobody could say anything about it: why a service is tier 1, what the
-- vendor said about the renewal, which laptop is being replaced. Assets keeps
-- comments on an object, and so does this.
CREATE TABLE service_asset_object_comments (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  object_id  UUID NOT NULL REFERENCES service_asset_objects(id) ON DELETE CASCADE,
  author_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body       TEXT NOT NULL CHECK(length(btrim(body)) BETWEEN 1 AND 10000),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX service_asset_object_comments_object
  ON service_asset_object_comments(object_id, created_at, id);
