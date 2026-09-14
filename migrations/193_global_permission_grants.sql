-- Global permissions are granted to groups or to everyone with product access,
-- as on Jira's global permissions page. Administer Jira stays with the
-- organization and site administrator roles. Sites keep the permissions every
-- member held before grants existed.
CREATE TABLE global_permission_grants (
  id BIGSERIAL PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  permission_key TEXT NOT NULL CHECK (length(permission_key) BETWEEN 1 AND 255 AND permission_key <> 'ADMINISTER'),
  group_id UUID REFERENCES groups(id) ON DELETE CASCADE,
  product_key TEXT CHECK (product_key IN ('jira-software', 'jira-service-management')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((group_id IS NULL) <> (product_key IS NULL))
);
CREATE UNIQUE INDEX global_permission_grants_holder
  ON global_permission_grants(workspace_id, permission_key, COALESCE(group_id::text, ''), COALESCE(product_key, ''));

INSERT INTO global_permission_grants(workspace_id, permission_key, product_key)
SELECT w.id, permission, 'jira-software'
FROM workspaces w
CROSS JOIN unnest(ARRAY['BULK_CHANGE','CREATE_SHARED_OBJECTS','SHARE_DASHBOARDS','USER_PICKER','BROWSE_USERS','CREATE_TEAM_MANAGED_PROJECT']) permission;

CREATE OR REPLACE FUNCTION provision_workspace_global_permissions()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO global_permission_grants(workspace_id, permission_key, product_key)
  SELECT NEW.id, permission, 'jira-software'
  FROM unnest(ARRAY['BULK_CHANGE','CREATE_SHARED_OBJECTS','SHARE_DASHBOARDS','USER_PICKER','BROWSE_USERS','CREATE_TEAM_MANAGED_PROJECT']) permission;
  RETURN NEW;
END;
$$;
CREATE TRIGGER provision_workspace_global_permissions_after_insert
AFTER INSERT ON workspaces FOR EACH ROW
EXECUTE FUNCTION provision_workspace_global_permissions();
