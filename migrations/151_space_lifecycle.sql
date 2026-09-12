-- Confluence spaces carry more than a key and a name: a type (a personal space
-- belongs to one person and is keyed ~accountId), an alias, a homepage, a
-- status, per-space settings and an optional theme.
ALTER TABLE wiki_spaces ADD COLUMN space_type TEXT NOT NULL DEFAULT 'global'
  CHECK (space_type IN ('global','personal'));
ALTER TABLE wiki_spaces ADD COLUMN alias TEXT NOT NULL DEFAULT '';
ALTER TABLE wiki_spaces ADD COLUMN homepage_id BIGINT;
ALTER TABLE wiki_spaces ADD COLUMN status TEXT NOT NULL DEFAULT 'current'
  CHECK (status IN ('current','archived'));
ALTER TABLE wiki_spaces ADD COLUMN route_override_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE wiki_spaces ADD COLUMN content_mode TEXT NOT NULL DEFAULT 'standard'
  CHECK (content_mode IN ('standard','compact'));
ALTER TABLE wiki_spaces ADD COLUMN theme_key TEXT NOT NULL DEFAULT '';

-- Confluence keys a personal space by the person it belongs to.
UPDATE wiki_spaces SET space_type = 'personal' WHERE key LIKE '~%';

CREATE INDEX wiki_spaces_type ON wiki_spaces(workspace_id, space_type, status);
