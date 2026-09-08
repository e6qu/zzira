CREATE TABLE organizations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sites (
  id UUID PRIMARY KEY,
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL UNIQUE REFERENCES workspaces(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, slug)
);

CREATE TABLE products (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  product_key TEXT NOT NULL CHECK (product_key IN ('jira-software', 'jira-service-management', 'confluence')),
  name TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (site_id, product_key)
);

CREATE TABLE directories (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  directory_type TEXT NOT NULL DEFAULT 'internal' CHECK (directory_type IN ('internal', 'scim')),
  active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, name)
);

CREATE TABLE directory_users (
  directory_id UUID NOT NULL REFERENCES directories(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (directory_id, user_id)
);

CREATE TABLE groups (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  directory_id UUID NOT NULL REFERENCES directories(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  description TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (directory_id, name)
);

CREATE TABLE group_members (
  group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, user_id)
);

CREATE TABLE role_bindings (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  scope_type TEXT NOT NULL CHECK (scope_type IN ('organization', 'site', 'product', 'project', 'space')),
  scope_id TEXT NOT NULL,
  role_key TEXT NOT NULL,
  principal_type TEXT NOT NULL CHECK (principal_type IN ('user', 'group')),
  principal_id TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'legacy-membership', 'system')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (scope_type, scope_id, role_key, principal_type, principal_id)
);
CREATE INDEX role_bindings_principal ON role_bindings (principal_type, principal_id);
CREATE INDEX role_bindings_scope ON role_bindings (scope_type, scope_id);

CREATE TABLE organization_audit_events (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  actor_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id TEXT NOT NULL,
  detail JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX organization_audit_events_lookup
  ON organization_audit_events (organization_id, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION provision_workspace_administration()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
  organization_uuid UUID;
  directory_uuid UUID;
BEGIN
  organization_uuid := NEW.cloud_id;
  INSERT INTO organizations (id, name) VALUES (organization_uuid, NEW.name);
  INSERT INTO sites (id, organization_id, workspace_id, slug, name)
    VALUES (NEW.cloud_id, organization_uuid, NEW.id, NEW.slug, NEW.name);
  INSERT INTO products (site_id, product_key, name) VALUES
    (NEW.cloud_id, 'jira-software', 'Jira Software'),
    (NEW.cloud_id, 'jira-service-management', 'Jira Service Management'),
    (NEW.cloud_id, 'confluence', 'Confluence');
  INSERT INTO directories (organization_id, name)
    VALUES (organization_uuid, NEW.name || ' users') RETURNING id INTO directory_uuid;
  INSERT INTO directory_users (directory_id, user_id)
    SELECT directory_uuid, m.user_id FROM memberships m WHERE m.workspace_id=NEW.id
    ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_workspace_administration_after_insert
AFTER INSERT ON workspaces
FOR EACH ROW EXECUTE FUNCTION provision_workspace_administration();

INSERT INTO organizations (id, name)
SELECT w.cloud_id, w.name
FROM workspaces w
ON CONFLICT DO NOTHING;

INSERT INTO sites (id, organization_id, workspace_id, slug, name)
SELECT w.cloud_id, w.cloud_id, w.id, w.slug, w.name
FROM workspaces w
WHERE NOT EXISTS (SELECT 1 FROM sites s WHERE s.workspace_id=w.id)
ON CONFLICT DO NOTHING;

INSERT INTO products (site_id, product_key, name)
SELECT s.id, product.product_key, product.name
FROM sites s
CROSS JOIN (VALUES
  ('jira-software', 'Jira Software'),
  ('jira-service-management', 'Jira Service Management'),
  ('confluence', 'Confluence')
) AS product(product_key, name)
ON CONFLICT DO NOTHING;

INSERT INTO directories (organization_id, name)
SELECT s.organization_id, s.name || ' users'
FROM sites s
ON CONFLICT DO NOTHING;

INSERT INTO directory_users (directory_id, user_id)
SELECT d.id, m.user_id
FROM memberships m
JOIN sites s ON s.workspace_id=m.workspace_id
JOIN directories d ON d.organization_id=s.organization_id
ON CONFLICT DO NOTHING;

CREATE OR REPLACE FUNCTION sync_membership_role_bindings()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
  membership_workspace TEXT;
  membership_user TEXT;
  membership_role TEXT;
  site_uuid UUID;
  organization_uuid UUID;
  directory_uuid UUID;
  workspace_name TEXT;
  workspace_slug TEXT;
  workspace_cloud_id UUID;
BEGIN
  membership_workspace := COALESCE(NEW.workspace_id, OLD.workspace_id);
  membership_user := COALESCE(NEW.user_id, OLD.user_id);

  IF TG_OP = 'UPDATE' AND (OLD.workspace_id <> NEW.workspace_id OR OLD.user_id <> NEW.user_id) THEN
    DELETE FROM role_bindings
      WHERE source='legacy-membership' AND principal_type='user' AND principal_id=OLD.user_id
        AND ((scope_type='site' AND scope_id IN (SELECT id::text FROM sites WHERE workspace_id=OLD.workspace_id))
          OR (scope_type='product' AND scope_id IN (
            SELECT p.id::text FROM products p JOIN sites s ON s.id=p.site_id WHERE s.workspace_id=OLD.workspace_id)));
    DELETE FROM directory_users du USING directories d, sites s
      WHERE du.directory_id=d.id AND d.organization_id=s.organization_id
        AND s.workspace_id=OLD.workspace_id AND du.user_id=OLD.user_id;
  END IF;

  DELETE FROM role_bindings
    WHERE source='legacy-membership' AND principal_type='user' AND principal_id=membership_user
      AND ((scope_type='site' AND scope_id IN (SELECT id::text FROM sites WHERE workspace_id=membership_workspace))
        OR (scope_type='product' AND scope_id IN (
          SELECT p.id::text FROM products p JOIN sites s ON s.id=p.site_id WHERE s.workspace_id=membership_workspace)));

  IF TG_OP = 'DELETE' THEN
    SELECT s.organization_id INTO organization_uuid FROM sites s WHERE s.workspace_id=membership_workspace;
    DELETE FROM directory_users du USING directories d
      WHERE du.directory_id=d.id AND d.organization_id=organization_uuid AND du.user_id=membership_user;
    RETURN OLD;
  END IF;

  membership_role := NEW.role;
  SELECT s.id, s.organization_id INTO site_uuid, organization_uuid
    FROM sites s WHERE s.workspace_id=membership_workspace;
  IF site_uuid IS NULL THEN
    SELECT w.name,w.slug,w.cloud_id INTO workspace_name,workspace_slug,workspace_cloud_id
      FROM workspaces w WHERE w.id=membership_workspace;
    organization_uuid := workspace_cloud_id;
    site_uuid := workspace_cloud_id;
    INSERT INTO organizations(id,name) VALUES(organization_uuid,workspace_name)
      ON CONFLICT(id) DO NOTHING;
    INSERT INTO sites(id,organization_id,workspace_id,slug,name)
      VALUES(site_uuid,organization_uuid,membership_workspace,workspace_slug,workspace_name)
      ON CONFLICT(workspace_id) DO NOTHING;
    INSERT INTO products(site_id,product_key,name) VALUES
      (site_uuid,'jira-software','Jira Software'),
      (site_uuid,'jira-service-management','Jira Service Management'),
      (site_uuid,'confluence','Confluence')
      ON CONFLICT DO NOTHING;
  END IF;
  SELECT d.id INTO directory_uuid FROM directories d
    WHERE d.organization_id=organization_uuid AND d.active ORDER BY d.created_at, d.id LIMIT 1;
  IF directory_uuid IS NULL THEN
    INSERT INTO directories(organization_id,name)
      VALUES(organization_uuid,(SELECT name FROM sites WHERE id=site_uuid) || ' users')
      RETURNING id INTO directory_uuid;
  END IF;

  INSERT INTO directory_users (directory_id, user_id)
    VALUES (directory_uuid, membership_user) ON CONFLICT DO NOTHING;
  INSERT INTO role_bindings (scope_type, scope_id, role_key, principal_type, principal_id, source)
    VALUES ('site', site_uuid::text,
      CASE WHEN membership_role='admin' THEN 'atlassian/site-admin' ELSE 'atlassian/site-user' END,
      'user', membership_user, 'legacy-membership')
    ON CONFLICT DO NOTHING;
  INSERT INTO role_bindings (scope_type, scope_id, role_key, principal_type, principal_id, source)
    SELECT 'product', p.id::text,
      CASE WHEN membership_role='admin' THEN 'atlassian/product-admin' ELSE 'atlassian/product-user' END,
      'user', membership_user, 'legacy-membership'
    FROM products p WHERE p.site_id=site_uuid
    ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

CREATE TRIGGER sync_membership_role_bindings_after_change
AFTER INSERT OR UPDATE OR DELETE ON memberships
FOR EACH ROW EXECUTE FUNCTION sync_membership_role_bindings();

INSERT INTO role_bindings (scope_type, scope_id, role_key, principal_type, principal_id, source)
SELECT 'site', s.id::text,
  CASE WHEN m.role='admin' THEN 'atlassian/site-admin' ELSE 'atlassian/site-user' END,
  'user', m.user_id, 'legacy-membership'
FROM memberships m JOIN sites s ON s.workspace_id=m.workspace_id
ON CONFLICT DO NOTHING;

INSERT INTO role_bindings (scope_type, scope_id, role_key, principal_type, principal_id, source)
SELECT 'product', p.id::text,
  CASE WHEN m.role='admin' THEN 'atlassian/product-admin' ELSE 'atlassian/product-user' END,
  'user', m.user_id, 'legacy-membership'
FROM memberships m
JOIN sites s ON s.workspace_id=m.workspace_id
JOIN products p ON p.site_id=s.id
ON CONFLICT DO NOTHING;
