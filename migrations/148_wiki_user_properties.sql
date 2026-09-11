-- Confluence keeps arbitrary app data against a user, separately from the
-- user's own profile.
CREATE TABLE wiki_user_properties (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
  value JSONB NOT NULL,
  version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);
