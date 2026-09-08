CREATE TABLE service_operations_settings (
  service_desk_id       TEXT PRIMARY KEY REFERENCES service_desks(id) ON DELETE CASCADE,
  cab_risk_threshold    SMALLINT NOT NULL DEFAULT 9 CHECK(cab_risk_threshold BETWEEN 1 AND 16),
  review_due_days       INTEGER NOT NULL DEFAULT 5 CHECK(review_due_days BETWEEN 1 AND 90),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE service_cab_members (
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY(service_desk_id,user_id)
);

CREATE SEQUENCE jira_service_on_call_shift_id START 1;
CREATE TABLE service_on_call_shifts (
  id              TEXT PRIMARY KEY DEFAULT nextval('jira_service_on_call_shift_id')::text,
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  label           TEXT NOT NULL,
  starts_at       TIMESTAMPTZ NOT NULL,
  ends_at         TIMESTAMPTZ NOT NULL,
  CHECK(ends_at > starts_at)
);
CREATE INDEX service_on_call_shifts_active ON service_on_call_shifts(service_desk_id,starts_at,ends_at);

CREATE TABLE service_request_operations (
  request_issue_id TEXT PRIMARY KEY REFERENCES service_requests(issue_id) ON DELETE CASCADE,
  kind             TEXT NOT NULL CHECK(kind IN ('incident','problem','change')),
  impact           SMALLINT NOT NULL DEFAULT 1 CHECK(impact BETWEEN 1 AND 4),
  likelihood       SMALLINT NOT NULL DEFAULT 1 CHECK(likelihood BETWEEN 1 AND 4),
  change_type      TEXT NOT NULL DEFAULT 'normal' CHECK(change_type IN ('standard','normal','emergency')),
  planned_start    TIMESTAMPTZ,
  planned_end      TIMESTAMPTZ,
  rollback_plan    TEXT NOT NULL DEFAULT '',
  on_call_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
  review_required  BOOLEAN NOT NULL DEFAULT FALSE,
  review_due_at    TIMESTAMPTZ,
  review_status    TEXT NOT NULL DEFAULT 'not_required' CHECK(review_status IN ('not_required','pending','in_progress','completed')),
  review_summary   TEXT NOT NULL DEFAULT '',
  updated_by       TEXT NOT NULL REFERENCES users(id),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK(planned_end IS NULL OR planned_start IS NULL OR planned_end > planned_start)
);
ALTER TABLE service_request_approvals ADD COLUMN automation_key TEXT;
CREATE UNIQUE INDEX service_request_approval_automation_once ON service_request_approvals(request_issue_id,automation_key)
WHERE automation_key IS NOT NULL;

CREATE FUNCTION seed_service_operations_settings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO service_operations_settings(service_desk_id) VALUES(NEW.id) ON CONFLICT DO NOTHING;
  RETURN NEW;
END $$;
CREATE TRIGGER seed_service_operations_settings_after_insert AFTER INSERT ON service_desks
FOR EACH ROW EXECUTE FUNCTION seed_service_operations_settings();
INSERT INTO service_operations_settings(service_desk_id) SELECT id FROM service_desks ON CONFLICT DO NOTHING;
