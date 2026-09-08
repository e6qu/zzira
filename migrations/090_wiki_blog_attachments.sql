ALTER TABLE wiki_attachments
  ADD COLUMN blog_post_id BIGINT REFERENCES wiki_blog_posts(id) ON DELETE CASCADE;

ALTER TABLE wiki_attachments
  ALTER COLUMN page_id DROP NOT NULL,
  DROP CONSTRAINT wiki_attachments_page_id_filename_key,
  ADD CONSTRAINT wiki_attachments_one_parent CHECK ((page_id IS NULL) <> (blog_post_id IS NULL)),
  ADD CONSTRAINT wiki_attachments_page_filename UNIQUE(page_id,filename),
  ADD CONSTRAINT wiki_attachments_blog_filename UNIQUE(blog_post_id,filename);

CREATE INDEX wiki_attachments_blog_post ON wiki_attachments(blog_post_id,status,id);
