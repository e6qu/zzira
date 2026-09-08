ALTER TABLE directory_users
  ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN suspended_at TIMESTAMPTZ,
  ADD COLUMN management_source TEXT NOT NULL DEFAULT 'invited'
    CHECK (management_source IN ('invited','synced'));

UPDATE directory_users du
SET active=u.active,
    suspended_at=CASE WHEN u.active THEN NULL ELSE now() END
FROM users u
WHERE u.id=du.user_id;

-- Before directory-scoped state existed, the global flag represented a
-- directory suspension. Preserve that state above, then restore the global
-- account so it can continue to participate in another organization.
UPDATE users u
SET active=TRUE
WHERE NOT u.active AND EXISTS (
  SELECT 1 FROM directory_users du WHERE du.user_id=u.id
);

ALTER TABLE users
  ADD COLUMN nickname TEXT NOT NULL DEFAULT '',
  ADD COLUMN job_title TEXT NOT NULL DEFAULT '',
  ADD COLUMN department TEXT NOT NULL DEFAULT '',
  ADD COLUMN organization_name TEXT NOT NULL DEFAULT '',
  ADD COLUMN location TEXT NOT NULL DEFAULT '',
  ADD COLUMN picture_url TEXT NOT NULL DEFAULT '',
  ADD COLUMN avatar_url TEXT NOT NULL DEFAULT '',
  ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN mfa_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN deactivated_at TIMESTAMPTZ;

CREATE INDEX directory_users_active_user
  ON directory_users (user_id, directory_id)
  WHERE active;
