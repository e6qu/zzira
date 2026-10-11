-- Null columns preserves the existing view's default set of visible columns.
ALTER TABLE plan_views ADD COLUMN columns JSONB NOT NULL DEFAULT 'null'
  CHECK (jsonb_typeof(columns) IN ('array','null'));
ALTER TABLE plan_views ADD COLUMN zoom TEXT NOT NULL DEFAULT 'standard'
  CHECK (zoom IN ('compact','standard','detailed'));
