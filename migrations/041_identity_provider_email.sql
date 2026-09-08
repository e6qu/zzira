ALTER TABLE oidc_identities
  ADD COLUMN email TEXT NOT NULL DEFAULT '';

UPDATE oidc_identities i SET email=u.email FROM users u WHERE u.id=i.user_id;
