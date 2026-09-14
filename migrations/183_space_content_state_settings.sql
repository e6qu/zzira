-- A space's administrators decide whether its pages carry content states and
-- which kinds: the states the space suggests, and the custom states writers
-- make as they go.
ALTER TABLE wiki_spaces
  ADD COLUMN content_states_allowed boolean NOT NULL DEFAULT true,
  ADD COLUMN custom_content_states_allowed boolean NOT NULL DEFAULT true,
  ADD COLUMN space_content_states_allowed boolean NOT NULL DEFAULT true;
