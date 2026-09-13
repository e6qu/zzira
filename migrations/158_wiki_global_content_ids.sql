-- Confluence content ids are unique across every kind of content: a page, a
-- blog post, a comment and an attachment never share one. Each of those tables
-- numbered its own rows, so page 1 and blog post 1 could both exist, and a
-- bare content id could name either. From here on they draw from one sequence.
--
-- The sequence starts above every id already in use, so no new content can
-- collide with old content of any kind. Hierarchical content (folders,
-- databases, whiteboards, custom content) already numbers from 10^12, well
-- clear of this range. Ids that already collide are left as they are, because
-- renumbering would break every link and reference a client has stored; the
-- resolver orders them deterministically instead.
CREATE SEQUENCE wiki_content_global_id AS BIGINT;

SELECT setval('wiki_content_global_id', GREATEST(
  (SELECT COALESCE(max(id), 0) FROM wiki_pages),
  (SELECT COALESCE(max(id), 0) FROM wiki_blog_posts),
  (SELECT COALESCE(max(id), 0) FROM wiki_footer_comments),
  (SELECT COALESCE(max(id), 0) FROM wiki_attachments),
  0
) + 1, false);

ALTER TABLE wiki_pages ALTER COLUMN id DROP IDENTITY;
ALTER TABLE wiki_pages ALTER COLUMN id SET DEFAULT nextval('wiki_content_global_id');
ALTER TABLE wiki_blog_posts ALTER COLUMN id DROP IDENTITY;
ALTER TABLE wiki_blog_posts ALTER COLUMN id SET DEFAULT nextval('wiki_content_global_id');
ALTER TABLE wiki_footer_comments ALTER COLUMN id DROP IDENTITY;
ALTER TABLE wiki_footer_comments ALTER COLUMN id SET DEFAULT nextval('wiki_content_global_id');
ALTER TABLE wiki_attachments ALTER COLUMN id DROP IDENTITY;
ALTER TABLE wiki_attachments ALTER COLUMN id SET DEFAULT nextval('wiki_content_global_id');
