-- Agent runs requested by a workflow transition's trigger-agent post function.
-- zzira records each request with the transition's action; it does not run an
-- agent itself.
CREATE TABLE workflow_agent_runs (
  id BIGSERIAL PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  issue_id TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  action_seq BIGINT NOT NULL,
  agent_id TEXT NOT NULL,
  prompt TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'requested' CHECK (status IN ('requested')),
  requested_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX workflow_agent_runs_issue ON workflow_agent_runs(workspace_id, issue_id, created_at);

-- Transitions are identified by Jira-style numeric ids. Earlier editors gave
-- new transitions generated ids, so renumber them, keeping a workflow's
-- published and draft definitions in step.
DO $$
DECLARE
  wf RECORD;
  mapping JSONB;
  next_id INT;
  old_id TEXT;
  rewrite JSONB;
BEGIN
  FOR wf IN SELECT id, def, draft_def FROM workflows LOOP
    mapping := '{}'::jsonb;
    SELECT COALESCE(max((t->>'id')::int), 1) INTO next_id
    FROM jsonb_array_elements(COALESCE(wf.def->'transitions','[]'::jsonb) || COALESCE(wf.draft_def->'transitions','[]'::jsonb)) t
    WHERE t->>'id' ~ '^[0-9]+$';
    FOR old_id IN
      SELECT DISTINCT t->>'id'
      FROM jsonb_array_elements(COALESCE(wf.def->'transitions','[]'::jsonb) || COALESCE(wf.draft_def->'transitions','[]'::jsonb)) WITH ORDINALITY AS e(t, n)
      WHERE NOT (t->>'id' ~ '^[0-9]+$')
      ORDER BY 1
    LOOP
      next_id := (next_id / 10 + 1) * 10 + 1;
      mapping := mapping || jsonb_build_object(old_id, next_id::text);
    END LOOP;
    IF mapping = '{}'::jsonb THEN
      CONTINUE;
    END IF;
    IF wf.def IS NOT NULL AND jsonb_typeof(wf.def->'transitions') = 'array' THEN
      SELECT jsonb_agg(CASE WHEN mapping ? (t->>'id') THEN jsonb_set(t, '{id}', mapping->(t->>'id')) ELSE t END ORDER BY n)
      INTO rewrite FROM jsonb_array_elements(wf.def->'transitions') WITH ORDINALITY AS e(t, n);
      UPDATE workflows SET def = jsonb_set(def, '{transitions}', COALESCE(rewrite, '[]'::jsonb)) WHERE id = wf.id;
    END IF;
    IF wf.draft_def IS NOT NULL AND jsonb_typeof(wf.draft_def->'transitions') = 'array' THEN
      SELECT jsonb_agg(CASE WHEN mapping ? (t->>'id') THEN jsonb_set(t, '{id}', mapping->(t->>'id')) ELSE t END ORDER BY n)
      INTO rewrite FROM jsonb_array_elements(wf.draft_def->'transitions') WITH ORDINALITY AS e(t, n);
      UPDATE workflows SET draft_def = jsonb_set(draft_def, '{transitions}', COALESCE(rewrite, '[]'::jsonb)) WHERE id = wf.id;
    END IF;
  END LOOP;
END $$;
