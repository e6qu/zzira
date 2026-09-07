ALTER TABLE wiki_footer_comments
  ADD COLUMN attachment_id BIGINT REFERENCES wiki_attachments(id) ON DELETE CASCADE;

ALTER TABLE wiki_footer_comments
  ALTER COLUMN page_id DROP NOT NULL;

ALTER TABLE wiki_footer_comments
  ADD CONSTRAINT wiki_footer_comments_target CHECK (num_nonnulls(page_id,attachment_id)=1);

CREATE INDEX wiki_footer_comments_attachment_parent
  ON wiki_footer_comments(attachment_id,parent_id,created_at,id)
  WHERE attachment_id IS NOT NULL;
