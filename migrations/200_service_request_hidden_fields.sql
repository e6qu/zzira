-- A request type field can be hidden from the customer portal and filled with a
-- preset value when a request is raised, as in Jira Service Management. Only
-- administrators see hidden fields, through expand=hiddenFields.
ALTER TABLE service_request_type_fields
  ADD COLUMN visible BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN preset_value JSONB;
