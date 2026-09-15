-- Service desk administrators turn Jira's customer notifications on or off;
-- every notification starts on.
ALTER TABLE service_desks ADD COLUMN disabled_customer_notifications TEXT[] NOT NULL DEFAULT '{}';
