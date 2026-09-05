-- Trashing a never-published draft must not make it visible to the space.
ALTER TABLE wiki_pages ADD COLUMN published BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE wiki_pages p SET published=EXISTS (
 SELECT 1 FROM wiki_page_versions v WHERE v.page_id=p.id AND v.status='current'
);
