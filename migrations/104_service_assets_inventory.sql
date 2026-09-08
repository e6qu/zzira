CREATE TABLE service_asset_schemas (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  assets_workspace_id UUID NOT NULL REFERENCES service_assets_workspaces(id) ON DELETE CASCADE,
  service_desk_id     TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  schema_key          TEXT NOT NULL CHECK(schema_key ~ '^[A-Z][A-Z0-9_]{0,31}$'),
  name                TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 255),
  description         TEXT NOT NULL DEFAULT '' CHECK(length(description) <= 2000),
  attributes          JSONB NOT NULL DEFAULT '[]',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(service_desk_id,schema_key),
  UNIQUE(service_desk_id,name)
);

CREATE TABLE service_asset_objects (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  schema_id  UUID NOT NULL REFERENCES service_asset_schemas(id) ON DELETE CASCADE,
  object_key TEXT NOT NULL CHECK(object_key ~ '^[A-Z][A-Z0-9_-]{0,63}$'),
  label      TEXT NOT NULL CHECK(length(label) BETWEEN 1 AND 255),
  values     JSONB NOT NULL DEFAULT '{}',
  x          INTEGER NOT NULL DEFAULT 40 CHECK(x BETWEEN 0 AND 5000),
  y          INTEGER NOT NULL DEFAULT 40 CHECK(y BETWEEN 0 AND 5000),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(schema_id,object_key)
);
CREATE INDEX service_asset_objects_schema ON service_asset_objects(schema_id,label,id);

CREATE TABLE service_asset_relationships (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  from_object_id UUID NOT NULL REFERENCES service_asset_objects(id) ON DELETE CASCADE,
  to_object_id   UUID NOT NULL REFERENCES service_asset_objects(id) ON DELETE CASCADE,
  relationship   TEXT NOT NULL CHECK(length(relationship) BETWEEN 1 AND 100),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK(from_object_id <> to_object_id),
  UNIQUE(service_desk_id,from_object_id,to_object_id,relationship)
);

CREATE TABLE service_request_assets (
  request_issue_id TEXT NOT NULL REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  object_id        UUID NOT NULL REFERENCES service_asset_objects(id) ON DELETE CASCADE,
  role             TEXT NOT NULL CHECK(role IN ('affected','depends_on')),
  linked_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(request_issue_id,object_id)
);
