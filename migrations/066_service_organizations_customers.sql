ALTER TABLE service_desks
  ADD COLUMN customer_access_open BOOLEAN NOT NULL DEFAULT TRUE;

CREATE SEQUENCE jira_service_organization_id START 1;

CREATE TABLE service_organizations (
  id           TEXT PRIMARY KEY DEFAULT nextval('jira_service_organization_id')::text,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX service_organizations_workspace_name
  ON service_organizations(workspace_id,lower(name));

CREATE TABLE service_organization_users (
  organization_id TEXT NOT NULL REFERENCES service_organizations(id) ON DELETE CASCADE,
  user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id,user_id)
);

CREATE TABLE service_organization_properties (
  organization_id TEXT NOT NULL REFERENCES service_organizations(id) ON DELETE CASCADE,
  key             TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
  value           JSONB NOT NULL,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id,key)
);

CREATE TABLE service_desk_organizations (
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  organization_id TEXT NOT NULL REFERENCES service_organizations(id) ON DELETE CASCADE,
  added_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (service_desk_id,organization_id)
);

CREATE TABLE service_desk_customers (
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  active          BOOLEAN NOT NULL DEFAULT TRUE,
  added_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (service_desk_id,user_id)
);

-- Customers who already raised a request are established customers of that desk.
INSERT INTO service_desk_customers(service_desk_id,user_id)
SELECT DISTINCT service_desk_id,customer_id FROM service_requests
ON CONFLICT DO NOTHING;
