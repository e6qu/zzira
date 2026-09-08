ALTER TABLE app_installations ADD COLUMN principal_id TEXT;

UPDATE app_installations SET principal_id='app_principal_' || id;

INSERT INTO users (id,email,password_hash,display_name,active)
SELECT principal_id,'app+' || id || '@apps.zzira.invalid','!app-principal!',name,status <> 'uninstalled'
FROM app_installations
ON CONFLICT (id) DO NOTHING;

ALTER TABLE app_installations ALTER COLUMN principal_id SET NOT NULL;
ALTER TABLE app_installations ADD CONSTRAINT app_installations_principal_unique UNIQUE(principal_id);
ALTER TABLE app_installations ADD CONSTRAINT app_installations_principal_fk
  FOREIGN KEY(principal_id) REFERENCES users(id);

INSERT INTO memberships(workspace_id,user_id,role)
SELECT workspace_id,principal_id,'member'
FROM app_installations
WHERE status <> 'uninstalled'
ON CONFLICT DO NOTHING;
