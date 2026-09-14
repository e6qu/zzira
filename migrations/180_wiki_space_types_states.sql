-- Confluence spaces come in more kinds than global and personal, and a
-- space can sit in the trash before it is deleted.
ALTER TABLE wiki_spaces DROP CONSTRAINT IF EXISTS wiki_spaces_space_type_check;
ALTER TABLE wiki_spaces ADD CONSTRAINT wiki_spaces_space_type_check
  CHECK (space_type IN ('global', 'collaboration', 'knowledge_base', 'personal', 'system', 'onboarding', 'xflow_sample_space'));
ALTER TABLE wiki_spaces DROP CONSTRAINT IF EXISTS wiki_spaces_status_check;
ALTER TABLE wiki_spaces ADD CONSTRAINT wiki_spaces_status_check
  CHECK (status IN ('current', 'archived', 'trashed'));
