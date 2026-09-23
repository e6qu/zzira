-- An object type may sit under another, as Assets object types do, so a
-- filter can ask for a type and everything beneath it.
ALTER TABLE service_asset_schemas
  ADD COLUMN parent_id UUID REFERENCES service_asset_schemas(id) ON DELETE SET NULL;

CREATE INDEX service_asset_schemas_parent ON service_asset_schemas(parent_id);
