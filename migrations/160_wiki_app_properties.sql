-- Forge app properties: JSON values an app keeps under its own keys, readable
-- and writable only by the app itself through asApp() requests. They belong to
-- the installation, so uninstalling the app takes them with it. They are kept
-- apart from the app's general storage so a property and a storage entry with
-- the same key never overwrite each other.
CREATE TABLE wiki_app_properties (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  key TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 127),
  value JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (installation_id, key)
);
