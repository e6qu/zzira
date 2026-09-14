-- App custom field configuration, Connect app migration and the service
-- registry.

-- The configuration a Forge custom field type keeps for each context of a
-- field. Ids are Jira's numbers.
CREATE SEQUENCE jira_app_field_configuration_id START 10000;

CREATE TABLE app_field_configurations (
  id            BIGINT PRIMARY KEY DEFAULT nextval('jira_app_field_configuration_id'),
  field_id      TEXT NOT NULL REFERENCES custom_fields(id) ON DELETE CASCADE,
  context_id    BIGINT NOT NULL REFERENCES custom_field_contexts(id) ON DELETE CASCADE,
  configuration JSONB,
  schema        JSONB,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (field_id, context_id)
);

-- Transfers an administrator opens for a Connect app to migrate its data.
CREATE TABLE app_migration_transfers (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  created_by      TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX app_migration_transfers_installation ON app_migration_transfers(installation_id, created_at DESC);

-- Background tasks moving a Connect issue field's data to its Forge custom
-- field.
CREATE TABLE connect_field_migrations (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_key      TEXT NOT NULL,
  task_id         TEXT NOT NULL REFERENCES api_tasks(id) ON DELETE CASCADE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (installation_id, module_key, task_id)
);

-- The service registry: the services a site's teams operate, with their tier.
CREATE TABLE service_registry_tiers (
  level       INT PRIMARY KEY,
  id          UUID NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  name_key    TEXT NOT NULL,
  description TEXT NOT NULL
);

INSERT INTO service_registry_tiers(level,id,name,name_key,description) VALUES
  (1,'1c7a8a2e-0c35-4b0b-9a0a-5f0d3b1d0001','Tier 1','service-registry.tier1.name','Critical services whose outage stops the business.'),
  (2,'1c7a8a2e-0c35-4b0b-9a0a-5f0d3b1d0002','Tier 2','service-registry.tier2.name','Important services with a significant customer impact.'),
  (3,'1c7a8a2e-0c35-4b0b-9a0a-5f0d3b1d0003','Tier 3','service-registry.tier3.name','Services with a limited impact.'),
  (4,'1c7a8a2e-0c35-4b0b-9a0a-5f0d3b1d0004','Tier 4','service-registry.tier4.name','Services with a minimal impact.');

CREATE TABLE service_registry_services (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
  description  TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
  tier_level   INT NOT NULL REFERENCES service_registry_tiers(level),
  revision     BIGINT NOT NULL DEFAULT 1,
  created_by   TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX service_registry_services_name ON service_registry_services(workspace_id, lower(name));
