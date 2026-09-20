-- The page that creates a token renders the secret in its own response: the
-- secret exists once and a redirect would either lose it or carry it in a URL,
-- where it would outlive the response in history and logs. A reload therefore
-- replays the POST, and without this it minted a second token with the same
-- label every time somebody refreshed.
--
-- The form carries the id of the request that made it. A replay names an id
-- the table already holds, so it creates nothing and the page says the token
-- was already made -- it cannot show the secret again, because only the hash
-- was kept.
ALTER TABLE api_tokens ADD COLUMN request_id TEXT;
CREATE UNIQUE INDEX api_tokens_user_request_id ON api_tokens (user_id, request_id)
  WHERE request_id IS NOT NULL;
