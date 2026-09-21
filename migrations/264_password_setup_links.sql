-- An account provisioned by an invitation has no password anyone knows, and a
-- person who forgets theirs has no way back in. A sign-in link is a one-time,
-- expiring secret that lets its holder set the password once: the link's hash
-- is stored, never the link.
CREATE TABLE password_setup_links (
  token_hash TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One live link per person: a new one replaces the last, so a link that was
-- sent and then re-sent cannot both be used.
CREATE UNIQUE INDEX password_setup_links_one_live ON password_setup_links (user_id) WHERE used_at IS NULL;
