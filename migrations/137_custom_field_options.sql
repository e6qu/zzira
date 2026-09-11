CREATE SEQUENCE jira_custom_field_option_id START WITH 10000;

-- Options belong to a context, so the same select field can offer different
-- choices in different projects or work types.
CREATE TABLE custom_field_options (
  id BIGINT PRIMARY KEY DEFAULT nextval('jira_custom_field_option_id'),
  context_id BIGINT NOT NULL REFERENCES custom_field_contexts(id) ON DELETE CASCADE,
  value TEXT NOT NULL CHECK (length(value) BETWEEN 1 AND 255),
  disabled BOOLEAN NOT NULL DEFAULT FALSE,
  position INTEGER NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX custom_field_options_context_value
  ON custom_field_options(context_id, lower(value));
CREATE INDEX custom_field_options_context_position
  ON custom_field_options(context_id, position, id);
