-- The help center's branding and home page announcement, which site
-- administrators customize as in Jira Service Management.
CREATE TABLE service_help_centers (
  workspace_id                 TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  name                         TEXT NOT NULL DEFAULT '',
  home_title                   TEXT NOT NULL DEFAULT '',
  logo_url                     TEXT NOT NULL DEFAULT '',
  banner_url                   TEXT NOT NULL DEFAULT '',
  banner_colour                TEXT NOT NULL DEFAULT '',
  banner_text_colour           TEXT NOT NULL DEFAULT '',
  navigation_background_colour TEXT NOT NULL DEFAULT '',
  navigation_text_colour       TEXT NOT NULL DEFAULT '',
  announcement_title           TEXT NOT NULL DEFAULT '',
  announcement_message         TEXT NOT NULL DEFAULT '' CHECK (length(announcement_message) <= 2000),
  updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now()
);
