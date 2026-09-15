-- People who can open a report can have its data emailed on a schedule, with
-- the same board, sprint, window and filters they chose. Each recipient
-- receives the report as they see it, and every scheduled run is a durable,
-- retried delivery like a dashboard email's.
CREATE TABLE report_subscriptions (
  id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id      TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  report            TEXT NOT NULL CHECK (length(report) BETWEEN 1 AND 2000),
  cron_expression   TEXT NOT NULL,
  recipients        JSONB NOT NULL DEFAULT '[]',
  enabled           BOOLEAN NOT NULL DEFAULT TRUE,
  next_run_at       TIMESTAMPTZ,
  last_run_at       TIMESTAMPTZ,
  last_result_count INTEGER,
  last_error        TEXT NOT NULL DEFAULT '',
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(workspace_id, user_id, report, cron_expression)
);
CREATE INDEX report_subscriptions_due
  ON report_subscriptions(next_run_at, id) WHERE enabled;

CREATE TABLE report_subscription_runs (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subscription_id BIGINT NOT NULL REFERENCES report_subscriptions(id) ON DELETE CASCADE,
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
CREATE INDEX report_subscription_runs_due
  ON report_subscription_runs(available_at, id);
