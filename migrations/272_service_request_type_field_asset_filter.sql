-- Jira narrows an Assets object field with AQL as well as with a schema, so a
-- form asks for this team's laptops rather than for every laptop. The filter
-- is stored as it was written; it is read and refused when it is saved, so a
-- form never offers a field whose filter the site cannot read.
ALTER TABLE service_request_type_fields
  ADD COLUMN asset_filter TEXT NOT NULL DEFAULT '' CHECK (length(asset_filter) <= 2000);
