-- Jira's issue field options carry arbitrary app properties and a per-option
-- scope, neither of which a Jira-created option has.
ALTER TABLE custom_field_options ADD COLUMN properties JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE custom_field_options ADD COLUMN scope JSONB;
