-- Time tracking providers installed apps declare through Connect's
-- jiraTimeTrackingProviders module. A provider's key is the app key and module
-- key joined by two underscores; its configuration page is the app's admin page
-- named by adminPageKey.
CREATE TABLE app_time_tracking_providers (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_key TEXT NOT NULL CHECK (module_key ~ '^[A-Za-z0-9-]{1,100}$'),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  admin_page_key TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (installation_id, module_key)
);
