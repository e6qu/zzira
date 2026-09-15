-- Project and global permissions installed apps declare through Connect's
-- jiraProjectPermissions and jiraGlobalPermissions modules. A permission's key
-- is the app key and module key joined by two underscores.
CREATE TABLE app_permission_modules (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_key TEXT NOT NULL CHECK (module_key ~ '^[A-Za-z0-9-]{1,100}$'),
  permission_type TEXT NOT NULL CHECK (permission_type IN ('PROJECT','GLOBAL')),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 1500),
  description TEXT NOT NULL CHECK (length(description) BETWEEN 1 AND 1500),
  category TEXT NOT NULL DEFAULT '',
  anonymous_allowed BOOLEAN NOT NULL DEFAULT FALSE,
  default_grants TEXT[] NOT NULL DEFAULT '{}',
  PRIMARY KEY (installation_id, module_key)
);
