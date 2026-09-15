-- A request type field can be shown only when a select or multi-select field
-- of the same form has one of the chosen options.
ALTER TABLE service_request_type_fields
  ADD COLUMN condition_field_id TEXT,
  ADD COLUMN condition_option_ids TEXT[] NOT NULL DEFAULT '{}',
  ADD CONSTRAINT service_request_type_fields_condition CHECK (condition_field_id IS NULL OR (condition_field_id <> field_id AND cardinality(condition_option_ids) > 0));
