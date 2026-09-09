CREATE TABLE user_login_state (
  user_id             TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  current_started_at  TIMESTAMPTZ NOT NULL,
  previous_started_at TIMESTAMPTZ,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Existing sessions predate start-time persistence. Treat migration time as
-- the current login boundary so upgraded accounts have deterministic behavior
-- until their next successful login establishes an exact previous boundary.
INSERT INTO user_login_state(user_id,current_started_at)
SELECT DISTINCT user_id,now() FROM sessions
ON CONFLICT(user_id) DO NOTHING;
