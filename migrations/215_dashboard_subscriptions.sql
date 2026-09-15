-- People who can view a dashboard can have it emailed on a schedule. Each
-- recipient receives the dashboard as they see it, and every scheduled run is
-- a durable, retried delivery like a filter subscription's.
CREATE TABLE dashboard_subscriptions (
  id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  dashboard_id      TEXT NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
  user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  cron_expression   TEXT NOT NULL,
  recipients        JSONB NOT NULL DEFAULT '[]',
  enabled           BOOLEAN NOT NULL DEFAULT TRUE,
  next_run_at       TIMESTAMPTZ,
  last_run_at       TIMESTAMPTZ,
  last_result_count INTEGER,
  last_error        TEXT NOT NULL DEFAULT '',
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(dashboard_id, user_id, cron_expression)
);
CREATE INDEX dashboard_subscriptions_due
  ON dashboard_subscriptions(next_run_at, id) WHERE enabled;

CREATE TABLE dashboard_subscription_runs (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subscription_id BIGINT NOT NULL REFERENCES dashboard_subscriptions(id) ON DELETE CASCADE,
  scheduled_for   TIMESTAMPTZ NOT NULL,
  state           TEXT NOT NULL DEFAULT 'PENDING'
                  CHECK (state IN ('PENDING','RUNNING','SUCCEEDED','FAILED')),
  attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  available_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at      TIMESTAMPTZ,
  completed_at    TIMESTAMPTZ,
  result_count    INTEGER,
  error           TEXT NOT NULL DEFAULT '',
  UNIQUE(subscription_id, scheduled_for)
);
CREATE INDEX dashboard_subscription_runs_due
  ON dashboard_subscription_runs(available_at, id);
