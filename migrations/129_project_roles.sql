CREATE SEQUENCE jira_project_role_id START WITH 10002;

CREATE TABLE project_roles (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  id BIGINT NOT NULL DEFAULT nextval('jira_project_role_id'),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255 AND name = btrim(name)),
  description TEXT NOT NULL DEFAULT '',
  is_admin BOOLEAN NOT NULL DEFAULT FALSE,
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, id)
);
CREATE UNIQUE INDEX project_roles_workspace_name
  ON project_roles (workspace_id, lower(name));

CREATE TABLE project_role_default_actors (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  role_id BIGINT NOT NULL,
  principal_type TEXT NOT NULL CHECK (principal_type IN ('user', 'group')),
  principal_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (workspace_id, role_id)
    REFERENCES project_roles(workspace_id, id) ON DELETE CASCADE,
  UNIQUE (workspace_id, role_id, principal_type, principal_id)
);
CREATE INDEX project_role_default_actors_principal
  ON project_role_default_actors (principal_type, principal_id);

INSERT INTO project_roles(workspace_id,id,name,description,is_admin,is_default)
SELECT id,10000,'Administrators','Administrators for a project.',TRUE,FALSE FROM workspaces
UNION ALL
SELECT id,10001,'Members','People who work in a project.',FALSE,TRUE FROM workspaces;

CREATE OR REPLACE FUNCTION provision_workspace_project_roles()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO project_roles(workspace_id,id,name,description,is_admin,is_default) VALUES
    (NEW.id,10000,'Administrators','Administrators for a project.',TRUE,FALSE),
    (NEW.id,10001,'Members','People who work in a project.',FALSE,TRUE);
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_workspace_project_roles_after_insert
AFTER INSERT ON workspaces
FOR EACH ROW EXECUTE FUNCTION provision_workspace_project_roles();

CREATE OR REPLACE FUNCTION provision_project_role_actors()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
  SELECT 'project',NEW.id,pr.id::text,'user',m.user_id,'system'
  FROM memberships m JOIN project_roles pr
    ON pr.workspace_id=m.workspace_id AND pr.is_default
  WHERE m.workspace_id=NEW.workspace_id
  ON CONFLICT DO NOTHING;

  INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
  SELECT 'project',NEW.id,pr.id::text,'user',m.user_id,'system'
  FROM memberships m JOIN project_roles pr
    ON pr.workspace_id=m.workspace_id AND pr.is_admin
  WHERE m.workspace_id=NEW.workspace_id AND m.role='admin'
  ON CONFLICT DO NOTHING;

  IF NEW.lead_account_id IS NOT NULL THEN
    INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
    SELECT 'project',NEW.id,pr.id::text,'user',NEW.lead_account_id,'system'
    FROM project_roles pr WHERE pr.workspace_id=NEW.workspace_id AND pr.is_admin
    ON CONFLICT DO NOTHING;
  END IF;

  INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
  SELECT 'project',NEW.id,d.role_id::text,d.principal_type,d.principal_id,'system'
  FROM project_role_default_actors d WHERE d.workspace_id=NEW.workspace_id
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_project_role_actors_after_insert
AFTER INSERT ON projects
FOR EACH ROW EXECUTE FUNCTION provision_project_role_actors();

CREATE OR REPLACE FUNCTION sync_new_member_project_roles()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    DELETE FROM role_bindings rb USING projects p
    WHERE rb.scope_type='project' AND rb.scope_id=p.id
      AND p.workspace_id=OLD.workspace_id AND rb.principal_type='user'
      AND rb.principal_id=OLD.user_id;
    RETURN OLD;
  END IF;

  IF TG_OP = 'UPDATE' AND (OLD.workspace_id <> NEW.workspace_id OR OLD.user_id <> NEW.user_id) THEN
    DELETE FROM role_bindings rb USING projects p
    WHERE rb.scope_type='project' AND rb.scope_id=p.id
      AND p.workspace_id=OLD.workspace_id AND rb.principal_type='user'
      AND rb.principal_id=OLD.user_id;
  END IF;

  INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
  SELECT 'project',p.id,pr.id::text,'user',NEW.user_id,'system'
  FROM projects p JOIN project_roles pr
    ON pr.workspace_id=p.workspace_id AND pr.is_default
  WHERE p.workspace_id=NEW.workspace_id AND p.lifecycle_state='ACTIVE'
  ON CONFLICT DO NOTHING;

  IF NEW.role='admin' THEN
    INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
    SELECT 'project',p.id,pr.id::text,'user',NEW.user_id,'system'
    FROM projects p JOIN project_roles pr
      ON pr.workspace_id=p.workspace_id AND pr.is_admin
    WHERE p.workspace_id=NEW.workspace_id AND p.lifecycle_state='ACTIVE'
    ON CONFLICT DO NOTHING;
  ELSE
    DELETE FROM role_bindings rb USING projects p,project_roles pr
    WHERE rb.scope_type='project' AND rb.scope_id=p.id
      AND p.workspace_id=NEW.workspace_id AND pr.workspace_id=p.workspace_id
      AND pr.id::text=rb.role_key AND pr.is_admin
      AND rb.principal_type='user' AND rb.principal_id=NEW.user_id
      AND rb.source='system' AND p.lead_account_id IS DISTINCT FROM NEW.user_id
      AND NOT EXISTS (
        SELECT 1 FROM project_role_default_actors d
        WHERE d.workspace_id=NEW.workspace_id AND d.role_id=pr.id
          AND d.principal_type='user' AND d.principal_id=NEW.user_id);
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER sync_new_member_project_roles_after_change
AFTER INSERT OR UPDATE OR DELETE ON memberships
FOR EACH ROW EXECUTE FUNCTION sync_new_member_project_roles();

CREATE OR REPLACE FUNCTION sync_project_lead_admin_role()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.lead_account_id IS NOT NULL AND OLD.lead_account_id IS DISTINCT FROM NEW.lead_account_id THEN
    DELETE FROM role_bindings rb USING project_roles pr
    WHERE rb.scope_type='project' AND rb.scope_id=NEW.id
      AND pr.workspace_id=NEW.workspace_id AND pr.id::text=rb.role_key AND pr.is_admin
      AND rb.principal_type='user' AND rb.principal_id=OLD.lead_account_id AND rb.source='system'
      AND NOT EXISTS (
        SELECT 1 FROM memberships m
        WHERE m.workspace_id=NEW.workspace_id AND m.user_id=OLD.lead_account_id AND m.role='admin')
      AND NOT EXISTS (
        SELECT 1 FROM project_role_default_actors d
        WHERE d.workspace_id=NEW.workspace_id AND d.role_id=pr.id
          AND d.principal_type='user' AND d.principal_id=OLD.lead_account_id);
  END IF;
  IF NEW.lead_account_id IS NOT NULL AND OLD.lead_account_id IS DISTINCT FROM NEW.lead_account_id THEN
    INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
    SELECT 'project',NEW.id,pr.id::text,'user',NEW.lead_account_id,'system'
    FROM project_roles pr WHERE pr.workspace_id=NEW.workspace_id AND pr.is_admin
    ON CONFLICT DO NOTHING;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER sync_project_lead_admin_role_after_update
AFTER UPDATE OF lead_account_id ON projects
FOR EACH ROW EXECUTE FUNCTION sync_project_lead_admin_role();

CREATE OR REPLACE FUNCTION cleanup_deleted_project_roles()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM role_bindings WHERE scope_type='project' AND scope_id=OLD.id;
  RETURN OLD;
END;
$$;

CREATE TRIGGER cleanup_deleted_project_roles_after_delete
AFTER DELETE ON projects
FOR EACH ROW EXECUTE FUNCTION cleanup_deleted_project_roles();

CREATE OR REPLACE FUNCTION cleanup_deleted_user_role_actors()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM project_role_default_actors
  WHERE principal_type='user' AND principal_id=OLD.id;
  DELETE FROM role_bindings
  WHERE principal_type='user' AND principal_id=OLD.id;
  RETURN OLD;
END;
$$;

CREATE TRIGGER cleanup_deleted_user_role_actors_before_delete
BEFORE DELETE ON users
FOR EACH ROW EXECUTE FUNCTION cleanup_deleted_user_role_actors();

CREATE OR REPLACE FUNCTION cleanup_deleted_group_role_actors()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM project_role_default_actors
  WHERE principal_type='group' AND principal_id=OLD.id::text;
  DELETE FROM role_bindings
  WHERE principal_type='group' AND principal_id=OLD.id::text;
  RETURN OLD;
END;
$$;

CREATE TRIGGER cleanup_deleted_group_role_actors_before_delete
BEFORE DELETE ON groups
FOR EACH ROW EXECUTE FUNCTION cleanup_deleted_group_role_actors();

INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
SELECT 'project',p.id,pr.id::text,'user',m.user_id,'system'
FROM projects p JOIN memberships m ON m.workspace_id=p.workspace_id
JOIN project_roles pr ON pr.workspace_id=p.workspace_id AND pr.is_default
ON CONFLICT DO NOTHING;

INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
SELECT 'project',p.id,pr.id::text,'user',m.user_id,'system'
FROM projects p JOIN memberships m ON m.workspace_id=p.workspace_id AND m.role='admin'
JOIN project_roles pr ON pr.workspace_id=p.workspace_id AND pr.is_admin
ON CONFLICT DO NOTHING;

INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
SELECT 'project',p.id,pr.id::text,'user',p.lead_account_id,'system'
FROM projects p JOIN project_roles pr ON pr.workspace_id=p.workspace_id AND pr.is_admin
WHERE p.lead_account_id IS NOT NULL
ON CONFLICT DO NOTHING;
