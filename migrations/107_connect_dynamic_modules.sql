ALTER TABLE app_modules ADD COLUMN dynamic BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE app_dynamic_modules (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_type      TEXT NOT NULL,
  module_key       TEXT NOT NULL,
  descriptor       JSONB NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(installation_id,module_key)
);
