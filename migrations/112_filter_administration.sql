ALTER TABLE filters ADD COLUMN IF NOT EXISTS columns TEXT[];
ALTER TABLE filters ADD COLUMN IF NOT EXISTS approximate_last_used TIMESTAMPTZ;

CREATE UNIQUE INDEX IF NOT EXISTS filters_owner_name_unique
  ON filters (workspace_id, owner_id, lower(name))
  WHERE owner_id IS NOT NULL;

CREATE TABLE filter_share_permissions (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  filter_id       TEXT NOT NULL REFERENCES filters(id) ON DELETE CASCADE,
  permission_type TEXT NOT NULL CHECK (permission_type IN
                    ('user','group','project','projectRole','global','authenticated')),
  account_id      TEXT REFERENCES users(id) ON DELETE CASCADE,
  group_id        UUID REFERENCES groups(id) ON DELETE CASCADE,
  project_id      TEXT REFERENCES projects(id) ON DELETE CASCADE,
  project_role_id TEXT,
  rights          INT NOT NULL DEFAULT 1 CHECK (rights IN (1,2)),
  CHECK (
    (permission_type='user' AND account_id IS NOT NULL AND group_id IS NULL AND project_id IS NULL AND project_role_id IS NULL) OR
    (permission_type='group' AND account_id IS NULL AND group_id IS NOT NULL AND project_id IS NULL AND project_role_id IS NULL) OR
    (permission_type='project' AND account_id IS NULL AND group_id IS NULL AND project_id IS NOT NULL AND project_role_id IS NULL) OR
    (permission_type='projectRole' AND account_id IS NULL AND group_id IS NULL AND project_id IS NOT NULL AND project_role_id IS NOT NULL) OR
    (permission_type IN ('global','authenticated') AND account_id IS NULL AND group_id IS NULL AND project_id IS NULL AND project_role_id IS NULL)
  )
);
CREATE UNIQUE INDEX filter_share_permissions_unique
  ON filter_share_permissions (
    filter_id,
    permission_type,
    COALESCE(account_id,''),
    COALESCE(group_id::TEXT,''),
    COALESCE(project_id,''),
    COALESCE(project_role_id,''),
    rights
  );
CREATE INDEX filter_share_permissions_filter ON filter_share_permissions(filter_id,id);

-- Preserve the historical built-in "All issues" filter as a visible system
-- filter. User-created filters remain private until explicitly shared.
INSERT INTO filter_share_permissions(filter_id,permission_type)
SELECT id,'authenticated' FROM filters WHERE owner_id IS NULL
ON CONFLICT DO NOTHING;

CREATE TABLE filter_default_share_scopes (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scope        TEXT NOT NULL DEFAULT 'PRIVATE'
               CHECK (scope IN ('PRIVATE','AUTHENTICATED')),
  PRIMARY KEY(workspace_id,user_id)
);

CREATE TABLE filter_subscriptions (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  filter_id       TEXT NOT NULL REFERENCES filters(id) ON DELETE CASCADE,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  cron_expression TEXT NOT NULL,
  recipients      JSONB NOT NULL DEFAULT '[]',
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  next_run_at     TIMESTAMPTZ,
  last_run_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(filter_id,user_id,cron_expression)
);
CREATE INDEX filter_subscriptions_due
  ON filter_subscriptions(next_run_at,id) WHERE enabled;
