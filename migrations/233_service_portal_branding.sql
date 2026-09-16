-- A service portal has Jira's introduction text and logo beside its name.
ALTER TABLE service_desks
  ADD COLUMN portal_description TEXT NOT NULL DEFAULT '' CHECK (length(portal_description) <= 1000),
  ADD COLUMN portal_logo_url TEXT NOT NULL DEFAULT '';
