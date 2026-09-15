-- Jira's Assets object field holds one object or several, chosen in the field's
-- context configuration rather than by using a different field type.
ALTER TABLE custom_field_contexts
  ADD COLUMN assets_multiple BOOLEAN NOT NULL DEFAULT FALSE;
