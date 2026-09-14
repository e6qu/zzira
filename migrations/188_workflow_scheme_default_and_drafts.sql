-- Every site has one default workflow scheme, the scheme Jira projects use
-- when no other is chosen; it cannot be edited or deleted. Drafts record who
-- last changed them and when.
ALTER TABLE workflow_schemes
  ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN draft_modified_at TIMESTAMPTZ,
  ADD COLUMN draft_modified_by TEXT REFERENCES users(id) ON DELETE SET NULL;

UPDATE workflow_schemes s SET is_default=true
WHERE s.name='Default workflow scheme' AND s.default_workflow_id='wf_default'
  AND s.id=(SELECT min(d.id) FROM workflow_schemes d WHERE d.workspace_id=s.workspace_id
            AND d.name='Default workflow scheme' AND d.default_workflow_id='wf_default');

CREATE UNIQUE INDEX workflow_schemes_one_default ON workflow_schemes(workspace_id) WHERE is_default;

CREATE OR REPLACE FUNCTION provision_default_workflow_scheme(workspace TEXT) RETURNS VOID LANGUAGE sql AS $$
  INSERT INTO workflow_schemes(id,workspace_id,name,description,default_workflow_id,is_default)
  SELECT 'scheme_default_' || workspace, workspace, 'Default workflow scheme',
         'The workflow scheme projects use when no other scheme is chosen.', 'wf_default', true
  WHERE NOT EXISTS (SELECT 1 FROM workflow_schemes WHERE workspace_id=workspace AND is_default)
    AND NOT EXISTS (SELECT 1 FROM workflow_schemes WHERE workspace_id=workspace AND name='Default workflow scheme')
$$;

SELECT provision_default_workflow_scheme(id) FROM workspaces;

CREATE OR REPLACE FUNCTION provision_default_workflow_scheme_for_workspace() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  PERFORM provision_default_workflow_scheme(NEW.id);
  RETURN NEW;
END;
$$;

CREATE TRIGGER provision_default_workflow_scheme_after_insert
AFTER INSERT ON workspaces
FOR EACH ROW EXECUTE FUNCTION provision_default_workflow_scheme_for_workspace();
