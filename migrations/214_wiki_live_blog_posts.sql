-- Blog posts are edited live as pages are, so a live document belongs to
-- either kind of content. Deleting the content ends its live document.
ALTER TABLE wiki_live_changes DROP CONSTRAINT wiki_live_changes_page_id_fkey;
ALTER TABLE wiki_live_changes DROP CONSTRAINT wiki_live_changes_pkey;
ALTER TABLE wiki_live_documents DROP CONSTRAINT wiki_live_documents_page_id_fkey;
ALTER TABLE wiki_live_documents DROP CONSTRAINT wiki_live_documents_pkey;

ALTER TABLE wiki_live_documents RENAME COLUMN page_id TO content_id;
ALTER TABLE wiki_live_documents ADD COLUMN content_type TEXT NOT NULL DEFAULT 'page' CHECK (content_type IN ('page','blogpost'));
ALTER TABLE wiki_live_documents ALTER COLUMN content_type DROP DEFAULT;
ALTER TABLE wiki_live_documents ADD PRIMARY KEY (content_type, content_id);

ALTER TABLE wiki_live_changes RENAME COLUMN page_id TO content_id;
ALTER TABLE wiki_live_changes ADD COLUMN content_type TEXT NOT NULL DEFAULT 'page';
ALTER TABLE wiki_live_changes ALTER COLUMN content_type DROP DEFAULT;
ALTER TABLE wiki_live_changes ADD PRIMARY KEY (content_type, content_id, revision);
ALTER TABLE wiki_live_changes ADD FOREIGN KEY (content_type, content_id)
  REFERENCES wiki_live_documents(content_type, content_id) ON DELETE CASCADE;

CREATE FUNCTION end_wiki_live_document() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM wiki_live_documents WHERE content_type = TG_ARGV[0] AND content_id = OLD.id;
  RETURN OLD;
END;
$$;
CREATE TRIGGER end_wiki_page_live_document AFTER DELETE ON wiki_pages
  FOR EACH ROW EXECUTE FUNCTION end_wiki_live_document('page');
CREATE TRIGGER end_wiki_blog_post_live_document AFTER DELETE ON wiki_blog_posts
  FOR EACH ROW EXECUTE FUNCTION end_wiki_live_document('blogpost');
