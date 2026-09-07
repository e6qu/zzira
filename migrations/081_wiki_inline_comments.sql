ALTER TABLE wiki_footer_comments
  ADD COLUMN comment_type TEXT NOT NULL DEFAULT 'footer'
    CHECK (comment_type IN ('footer','inline')),
  ADD COLUMN inline_selection TEXT NOT NULL DEFAULT '',
  ADD COLUMN inline_match_count INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN inline_match_index INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN inline_marker_ref TEXT NOT NULL DEFAULT '',
  ADD COLUMN resolution_status TEXT NOT NULL DEFAULT 'open'
    CHECK (resolution_status IN ('open','reopened','resolved','dangling')),
  ADD COLUMN resolution_modifier_id TEXT REFERENCES users(id),
  ADD COLUMN resolution_modified_at TIMESTAMPTZ,
  ADD CONSTRAINT wiki_inline_comment_shape CHECK (
    comment_type='footer' OR (
      attachment_id IS NULL AND inline_selection<>'' AND
      inline_match_count>0 AND inline_match_index>=0 AND
      inline_match_index<inline_match_count AND inline_marker_ref<>''
    )
  );

CREATE INDEX wiki_inline_comments_page_resolution
  ON wiki_footer_comments(page_id,resolution_status,created_at,id)
  WHERE comment_type='inline';
