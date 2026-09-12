-- Confluence's look and feel is set for the site and may be overridden for one
-- space, and each says whether it is showing the global settings, its own
-- custom ones, or the theme's.
CREATE TABLE wiki_look_and_feel (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  space_id BIGINT REFERENCES wiki_spaces(id) ON DELETE CASCADE,
  selected TEXT NOT NULL DEFAULT 'global' CHECK (selected IN ('global','custom','theme')),
  custom JSONB,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One row for the site, and at most one per space.
CREATE UNIQUE INDEX wiki_look_and_feel_site
  ON wiki_look_and_feel(workspace_id) WHERE space_id IS NULL;
CREATE UNIQUE INDEX wiki_look_and_feel_space
  ON wiki_look_and_feel(workspace_id, space_id) WHERE space_id IS NOT NULL;

CREATE TABLE wiki_site_settings (
  workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
  global_theme_key TEXT NOT NULL DEFAULT ''
);
