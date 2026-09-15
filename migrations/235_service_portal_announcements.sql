-- A portal lets its agents add an announcement, as Jira Service Management's
-- portal setting "agents can add announcements to this portal" does.
ALTER TABLE service_desks
  ADD COLUMN announcements_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN announcement_title TEXT NOT NULL DEFAULT '',
  ADD COLUMN announcement_message TEXT NOT NULL DEFAULT '' CHECK (length(announcement_message) <= 2000);
