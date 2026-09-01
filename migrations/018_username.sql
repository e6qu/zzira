-- The signed-in account's display handle, distinct from display_name (a
-- freeform "how do you want to be addressed" field) and email (an identity
-- credential, not a public handle). Populated from the OIDC identity
-- provider's preferred_username claim on sign-in; NULL until then, in which
-- case callers fall back to the email's local part.
ALTER TABLE users ADD COLUMN username TEXT;
