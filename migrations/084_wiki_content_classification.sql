ALTER TABLE wiki_content
  ADD COLUMN private BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN classification_level TEXT NOT NULL DEFAULT '';

CREATE INDEX wiki_content_private ON wiki_content(space_id,private,author_id,status);
