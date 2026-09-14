-- Confluence keeps pages, folders, whiteboards, databases and Smart Links in
-- one content tree: any of them can hold any other, and siblings of every kind
-- share one order. Content could already sit beneath a page; a page could not
-- sit beneath content, and content had no stored order of its own, so a folder
-- holding a page and a whiteboard had nothing to say which came first.

ALTER TABLE wiki_pages ADD COLUMN parent_content_id BIGINT REFERENCES wiki_content(id) ON DELETE RESTRICT;
ALTER TABLE wiki_pages ADD CONSTRAINT wiki_pages_one_parent CHECK (num_nonnulls(parent_id, parent_content_id) <= 1);
CREATE INDEX wiki_pages_parent_content ON wiki_pages(parent_content_id, position, id);

ALTER TABLE wiki_content ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
CREATE INDEX wiki_content_sibling_order ON wiki_content(space_id, parent_page_id, parent_content_id, position, id);

-- The tree across both tables. Content ids are unique across pages and
-- content, so a parent id alone names its node.
CREATE VIEW wiki_tree_nodes AS
  SELECT p.id, 'page'::text AS type, p.space_id, COALESCE(p.parent_id, p.parent_content_id) AS parent_id,
         p.status, p.position, p.title
  FROM wiki_pages p
  UNION ALL
  SELECT c.id, c.type, c.space_id, COALESCE(c.parent_content_id, c.parent_page_id) AS parent_id,
         c.status, c.position, c.title
  FROM wiki_content c
  WHERE c.type IN ('folder', 'whiteboard', 'database', 'embed');

-- Siblings of every kind share one order. Pages keep the order readers were
-- already seeing and come before content that shares their parent, which keeps
-- its id order.
WITH ordered AS (
  SELECT id, row_number() OVER (
    PARTITION BY space_id, parent_id
    ORDER BY CASE WHEN type = 'page' THEN 0 ELSE 1 END, position, id) AS rank
  FROM wiki_tree_nodes
)
UPDATE wiki_pages SET position = ordered.rank FROM ordered WHERE wiki_pages.id = ordered.id;
WITH ordered AS (
  SELECT id, row_number() OVER (
    PARTITION BY space_id, parent_id
    ORDER BY CASE WHEN type = 'page' THEN 0 ELSE 1 END, position, id) AS rank
  FROM wiki_tree_nodes
)
UPDATE wiki_content SET position = ordered.rank FROM ordered WHERE wiki_content.id = ordered.id;
