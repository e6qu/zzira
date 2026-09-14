-- A redaction can be restored: the removed text is kept with the redaction,
-- apart from text redacted inside a code block, which Confluence does not
-- restore. Earlier redactions did not keep their text and stay unrestorable.
ALTER TABLE wiki_page_redactions
  ADD COLUMN original_text text,
  ADD COLUMN restorable boolean NOT NULL DEFAULT false,
  ADD COLUMN restored_at timestamptz,
  ADD COLUMN restored_by text REFERENCES users(id);
ALTER TABLE wiki_blog_post_redactions
  ADD COLUMN original_text text,
  ADD COLUMN restorable boolean NOT NULL DEFAULT false,
  ADD COLUMN restored_at timestamptz,
  ADD COLUMN restored_by text REFERENCES users(id);
