ALTER TABLE wiki_spaces
  ADD COLUMN default_classification_level TEXT NOT NULL DEFAULT ''
  CHECK(default_classification_level IN ('','public','internal','confidential','restricted'));
