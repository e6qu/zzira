-- Jira Service Management identifies each organization with a system
-- generated UUID alongside its numeric id.
ALTER TABLE service_organizations ADD COLUMN uuid UUID NOT NULL DEFAULT gen_random_uuid();
