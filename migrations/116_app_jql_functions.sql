CREATE TABLE app_jql_function_modules (
  id                BIGSERIAL PRIMARY KEY,
  installation_id   TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_key        TEXT NOT NULL,
  function_name     TEXT NOT NULL,
  path              TEXT NOT NULL,
  arguments         JSONB NOT NULL DEFAULT '[]',
  types             TEXT[] NOT NULL,
  operators         TEXT[] NOT NULL,
  UNIQUE(installation_id,module_key),
  UNIQUE(installation_id,function_name)
);

CREATE INDEX app_jql_function_modules_name
  ON app_jql_function_modules(lower(function_name),installation_id);
