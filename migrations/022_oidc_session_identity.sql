-- A provider session identifier is unique only within its issuer, and a
-- subject logout must not revoke password sessions owned by the same local
-- user. Persist the verified OIDC identity on each OIDC-backed session so
-- back-channel logout can target exactly the sessions established by that OP.
ALTER TABLE sessions ADD COLUMN oidc_issuer TEXT;
ALTER TABLE sessions ADD COLUMN oidc_subject TEXT;

UPDATE sessions s
SET oidc_issuer = i.issuer,
    oidc_subject = i.subject
FROM oidc_identities i
WHERE s.user_id = i.user_id
  AND s.oidc_id_token IS NOT NULL;

CREATE INDEX sessions_oidc_issuer_sid_idx
  ON sessions(oidc_issuer, oidc_session_id)
  WHERE oidc_issuer IS NOT NULL AND oidc_session_id IS NOT NULL;

CREATE INDEX sessions_oidc_identity_idx
  ON sessions(oidc_issuer, oidc_subject)
  WHERE oidc_issuer IS NOT NULL AND oidc_subject IS NOT NULL;
