-- Saved plan views keep the custom filters as well as their keyword filter.
ALTER TABLE plan_views ADD COLUMN custom_filters JSONB NOT NULL DEFAULT '{}'
  CHECK (jsonb_typeof(custom_filters) = 'object');
