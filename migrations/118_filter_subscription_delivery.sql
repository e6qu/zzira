ALTER TABLE email_outbox ADD COLUMN dedupe_key TEXT;
CREATE UNIQUE INDEX email_outbox_dedupe ON email_outbox(dedupe_key) WHERE dedupe_key IS NOT NULL;

ALTER TABLE filter_subscriptions
  ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC',
  ADD COLUMN last_error TEXT NOT NULL DEFAULT '',
  ADD COLUMN last_result_count INTEGER;

CREATE TABLE filter_subscription_runs (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subscription_id BIGINT NOT NULL REFERENCES filter_subscriptions(id) ON DELETE CASCADE,
  scheduled_for   TIMESTAMPTZ NOT NULL,
  state           TEXT NOT NULL DEFAULT 'PENDING'
                  CHECK (state IN ('PENDING','RUNNING','SUCCEEDED','FAILED')),
  attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  available_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at      TIMESTAMPTZ,
  completed_at    TIMESTAMPTZ,
  result_count    INTEGER,
  error           TEXT NOT NULL DEFAULT '',
  UNIQUE(subscription_id,scheduled_for)
);

CREATE INDEX filter_subscription_runs_due
  ON filter_subscription_runs(available_at,id)
  WHERE state IN ('PENDING','RUNNING','FAILED');
