-- Entity property indexes installed apps declare through Connect's
-- jiraEntityProperties module, statically or as dynamic modules. Each row is
-- one extraction: the value at object_name inside the property property_key of
-- an entity, indexed as a number, string, text, date or user and optionally
-- searchable under an alias. JQL searches issue properties through them as
-- issue.property[property_key].object_name.
CREATE TABLE app_entity_property_indexes (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_key TEXT NOT NULL CHECK (length(module_key) BETWEEN 1 AND 100),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  entity_type TEXT NOT NULL CHECK (entity_type IN ('issue','comment','project','user','issuetype')),
  property_key TEXT NOT NULL CHECK (length(property_key) BETWEEN 1 AND 255),
  object_name TEXT NOT NULL CHECK (length(object_name) BETWEEN 1 AND 255),
  extraction_type TEXT NOT NULL CHECK (extraction_type IN ('number','string','text','date','user')),
  alias TEXT NOT NULL DEFAULT '',
  dynamic BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY (installation_id, entity_type, property_key, object_name)
);
CREATE INDEX app_entity_property_indexes_module ON app_entity_property_indexes (installation_id, module_key);

-- Property values are arbitrary JSON, so a query comparing them as numbers or
-- dates must not fail on a value that is neither; these casts give NULL instead.
CREATE OR REPLACE FUNCTION jql_try_numeric(value TEXT) RETURNS NUMERIC
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
  RETURN value::numeric;
EXCEPTION WHEN others THEN
  RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION jql_try_timestamptz(value TEXT) RETURNS TIMESTAMPTZ
LANGUAGE plpgsql STABLE AS $$
BEGIN
  RETURN value::timestamptz;
EXCEPTION WHEN others THEN
  RETURN NULL;
END;
$$;

-- A dynamic module keeps the module zzira translated it into, so upgrading the
-- app can restore dynamic modules without re-parsing Connect descriptors.
ALTER TABLE app_dynamic_modules ADD COLUMN translated JSONB NOT NULL DEFAULT '{}'::jsonb;
