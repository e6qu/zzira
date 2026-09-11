-- Custom content existed only as a read-only child of a page or a blog post,
-- in two tables that could not express the same thing in a space or under
-- other custom content. It belongs in wiki_content with everything else that
-- lives in a space, so one model answers for all of it.
ALTER TABLE wiki_content DROP CONSTRAINT wiki_content_type_check;
ALTER TABLE wiki_content ADD CONSTRAINT wiki_content_type_check
  CHECK (type IN ('folder','database','embed','whiteboard','custom'));
ALTER TABLE wiki_content ADD COLUMN custom_type TEXT REFERENCES wiki_custom_content_types(type);
ALTER TABLE wiki_content ADD COLUMN body TEXT NOT NULL DEFAULT '';
ALTER TABLE wiki_content ADD COLUMN parent_blog_post_id BIGINT REFERENCES wiki_blog_posts(id) ON DELETE CASCADE;
ALTER TABLE wiki_content DROP CONSTRAINT wiki_content_check;
ALTER TABLE wiki_content ADD CONSTRAINT wiki_content_check
  CHECK (num_nonnulls(parent_page_id, parent_content_id, parent_blog_post_id) <= 1);
-- Only custom content carries an app-defined type, and it always carries one.
ALTER TABLE wiki_content ADD CONSTRAINT wiki_content_custom_type
  CHECK ((type = 'custom') = (custom_type IS NOT NULL));
CREATE INDEX wiki_content_custom ON wiki_content(space_id, custom_type, status, id);
CREATE INDEX wiki_content_parent_blog ON wiki_content(parent_blog_post_id, status, id);

-- A version keeps the body it had, so an earlier version can be read back.
ALTER TABLE wiki_content_versions ADD COLUMN body TEXT NOT NULL DEFAULT '';

CREATE TABLE wiki_content_labels (
  content_id BIGINT NOT NULL REFERENCES wiki_content(id) ON DELETE CASCADE,
  label_id BIGINT NOT NULL REFERENCES wiki_labels(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (content_id, label_id)
);

INSERT INTO wiki_content(space_id,parent_page_id,root_page_id,type,custom_type,title,body,author_id,owner_id,version,created_at)
  SELECT p.space_id, cc.page_id, cc.page_id, 'custom', cc.type, cc.title, cc.body, cc.author_id, cc.author_id, cc.version, cc.created_at
  FROM wiki_page_custom_content cc JOIN wiki_pages p ON p.id = cc.page_id;
INSERT INTO wiki_content(space_id,parent_blog_post_id,type,custom_type,title,body,author_id,owner_id,version,created_at)
  SELECT b.space_id, cc.blog_post_id, 'custom', cc.type, cc.title, cc.body, cc.author_id, cc.author_id, cc.version, cc.created_at
  FROM wiki_blog_custom_content cc JOIN wiki_blog_posts b ON b.id = cc.blog_post_id;
INSERT INTO wiki_content_versions(content_id,version,title,status,author_id,body,created_at)
  SELECT id, version, title, status, author_id, body, created_at FROM wiki_content WHERE type = 'custom';

DROP TABLE wiki_page_custom_content;
DROP TABLE wiki_blog_custom_content;
