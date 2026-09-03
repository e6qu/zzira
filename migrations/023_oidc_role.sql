-- The Shauth-issued OIDC role claim (developer/admin), distinct from a
-- user's ZZIRA workspace membership role (member/admin): the identity
-- provider's role is what /auth/validation must expose as
-- data-testid="validation-role", since that is a machine-readable identity
-- contract other systems assert against, not a ZZIRA authorization decision.
-- Populated from the OIDC identity provider's role claim on sign-in; NULL
-- until then or when signing in without SSO.
ALTER TABLE users ADD COLUMN oidc_role TEXT;
