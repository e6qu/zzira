-- Confluence moves a page before or after a sibling, which needs the children
-- of a page to have an order. They were returned in id order, so there was
-- nothing to move within; the position is seeded from that same order, which
-- is what readers were already seeing.
ALTER TABLE wiki_pages ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
UPDATE wiki_pages SET position = ordered.rank
  FROM (SELECT id, row_number() OVER (PARTITION BY space_id, parent_id ORDER BY id) AS rank
        FROM wiki_pages) ordered
  WHERE wiki_pages.id = ordered.id;
CREATE INDEX wiki_pages_sibling_order ON wiki_pages(space_id, parent_id, position, id);

-- Archiving is a status of its own: an archived page keeps its place and its
-- history but leaves the space's current content.
ALTER TABLE wiki_pages DROP CONSTRAINT wiki_pages_status_check;
ALTER TABLE wiki_pages ADD CONSTRAINT wiki_pages_status_check
  CHECK (status IN ('current','draft','trashed','archived'));
