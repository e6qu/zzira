-- A star is Confluence's favourite relation, the link from a user to content
-- that the relations API reads and CQL's favourite field searches. Stars kept
-- in a table of their own were invisible to both, so they become relations.
INSERT INTO wiki_relations(workspace_id,name,source_type,source_key,source_status,source_version,target_type,target_key,target_status,target_version,created_by,created_at)
  SELECT workspace_id,'favourite','user',user_id,'current',0,'content',page_id::text,'current',0,user_id,created_at
  FROM wiki_favourites
  ON CONFLICT DO NOTHING;
DROP TABLE wiki_favourites;
DROP TABLE IF EXISTS wiki_blog_post_favourites;

-- Custom content takes footer comments as pages, blog posts and attachments do.
ALTER TABLE wiki_footer_comments ADD COLUMN custom_content_id BIGINT REFERENCES wiki_content(id) ON DELETE CASCADE;
ALTER TABLE wiki_footer_comments DROP CONSTRAINT wiki_footer_comments_target;
ALTER TABLE wiki_footer_comments ADD CONSTRAINT wiki_footer_comments_target
  CHECK (num_nonnulls(page_id, attachment_id, blog_post_id, custom_content_id) = 1);
CREATE INDEX wiki_footer_comments_custom_content ON wiki_footer_comments(custom_content_id, created_at, id)
  WHERE custom_content_id IS NOT NULL;
