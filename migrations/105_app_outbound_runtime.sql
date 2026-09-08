CREATE TABLE app_lifecycle_callbacks (
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  event           TEXT NOT NULL CHECK(event IN ('installed','enabled','disabled','upgraded','uninstalled')),
  path            TEXT NOT NULL CHECK(left(path,1)='/'),
  PRIMARY KEY(installation_id,event)
);

CREATE TABLE app_webhook_modules (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_key      TEXT NOT NULL,
  path            TEXT NOT NULL CHECK(left(path,1)='/'),
  events          TEXT[] NOT NULL CHECK(cardinality(events)>0),
  jql             TEXT NOT NULL DEFAULT '',
  last_seq        BIGINT NOT NULL DEFAULT 0,
  UNIQUE(installation_id,module_key)
);

CREATE TABLE app_scheduled_triggers (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  installation_id  TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  module_key       TEXT NOT NULL,
  path             TEXT NOT NULL CHECK(left(path,1)='/'),
  interval_name    TEXT NOT NULL CHECK(interval_name IN ('fiveMinute','hour','day','week')),
  interval_seconds INTEGER NOT NULL CHECK(interval_seconds IN (300,3600,86400,604800)),
  next_run_at      TIMESTAMPTZ NOT NULL DEFAULT now()+interval '5 minutes',
  UNIQUE(installation_id,module_key)
);
CREATE INDEX app_scheduled_triggers_due ON app_scheduled_triggers(next_run_at,id);

CREATE TABLE app_outbound_deliveries (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  installation_id TEXT NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
  kind            TEXT NOT NULL CHECK(kind IN ('lifecycle','webhook','scheduled')),
  module_key      TEXT NOT NULL DEFAULT '',
  event           TEXT NOT NULL,
  path            TEXT NOT NULL CHECK(left(path,1)='/'),
  payload         JSONB NOT NULL,
  dedupe_key      TEXT NOT NULL,
  state           TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','delivering','delivered','failed')),
  attempts        INTEGER NOT NULL DEFAULT 0,
  available_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at      TIMESTAMPTZ,
  completed_at    TIMESTAMPTZ,
  response_code   INTEGER,
  last_error      TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(installation_id,dedupe_key)
);
CREATE INDEX app_outbound_deliveries_due ON app_outbound_deliveries(state,available_at,created_at,id);
CREATE INDEX app_outbound_deliveries_installation ON app_outbound_deliveries(installation_id,created_at DESC,id DESC);
