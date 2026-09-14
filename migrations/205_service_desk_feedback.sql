-- A service desk administrator can turn customer satisfaction feedback off
-- for a service desk; desks ask for it by default.
ALTER TABLE service_desks ADD COLUMN feedback_enabled BOOLEAN NOT NULL DEFAULT TRUE;
