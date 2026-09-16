-- Jira scopes an Assets object field to one schema, so a form asks for a
-- laptop rather than for anything in the service project's inventory.
ALTER TABLE service_request_type_fields
  ADD COLUMN asset_schema_id UUID REFERENCES service_asset_schemas(id) ON DELETE SET NULL;
