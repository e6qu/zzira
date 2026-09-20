-- An API token a person creates for themselves has to be recognisable later:
-- which one is this, when did it start, and is it still good? The table kept
-- only the label and an optional expiry, because until now tokens were made
-- by the server's seed mode and never listed back.
ALTER TABLE api_tokens ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();
