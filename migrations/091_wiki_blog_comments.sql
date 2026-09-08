ALTER TABLE wiki_footer_comments
  ADD COLUMN blog_post_id BIGINT REFERENCES wiki_blog_posts(id) ON DELETE CASCADE;

ALTER TABLE wiki_footer_comments
  DROP CONSTRAINT wiki_footer_comments_target,
  DROP CONSTRAINT wiki_inline_comment_shape,
  ADD CONSTRAINT wiki_footer_comments_target CHECK (num_nonnulls(page_id,attachment_id,blog_post_id)=1),
  ADD CONSTRAINT wiki_inline_comment_shape CHECK (
    comment_type='footer' OR (
      attachment_id IS NULL AND inline_selection<>'' AND
      inline_match_count>0 AND inline_match_index>=0 AND
      inline_match_index<inline_match_count AND inline_marker_ref<>''
    )
  );

CREATE INDEX wiki_footer_comments_blog_parent
  ON wiki_footer_comments(blog_post_id,comment_type,parent_id,created_at,id)
  WHERE blog_post_id IS NOT NULL;
