-- Built-in issue events take Jira's EventType ids: 7 reopened, 8 deleted,
-- 9 moved, 10-12 work logged/started/stopped, 13 generic, 14 comment edited,
-- 15-16 worklog updated/deleted. Earlier releases numbered them differently.
CREATE FUNCTION jira_issue_event_id_remap(old BIGINT) RETURNS BIGINT
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE old WHEN 7 THEN 14 WHEN 8 THEN 7 WHEN 9 THEN 8 WHEN 10 THEN 9
    WHEN 11 THEN 10 WHEN 12 THEN 11 WHEN 13 THEN 12 WHEN 14 THEN 15
    WHEN 15 THEN 16 WHEN 16 THEN 13 ELSE old END
$$;

-- Two passes keep the unique indexes satisfied while ids swap places.
UPDATE notification_scheme_entries SET event_id=event_id+1000000 WHERE event_id BETWEEN 7 AND 16;
UPDATE notification_scheme_entries SET event_id=jira_issue_event_id_remap(event_id-1000000) WHERE event_id BETWEEN 1000007 AND 1000016;
UPDATE notification_event_deliveries SET event_id=event_id+1000000 WHERE event_id BETWEEN 7 AND 16;
UPDATE notification_event_deliveries SET event_id=jira_issue_event_id_remap(event_id-1000000) WHERE event_id BETWEEN 1000007 AND 1000016;

CREATE FUNCTION jira_issue_event_transitions_remap(def JSONB) RETURNS JSONB
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE WHEN def IS NULL OR jsonb_typeof(def->'transitions') IS DISTINCT FROM 'array' THEN def
  ELSE jsonb_set(def, '{transitions}', COALESCE((
    SELECT jsonb_agg(CASE WHEN t->>'customIssueEventId' ~ '^[0-9]{1,2}$'
      THEN jsonb_set(t, '{customIssueEventId}', to_jsonb(jira_issue_event_id_remap((t->>'customIssueEventId')::BIGINT)::text))
      ELSE t END ORDER BY ord)
    FROM jsonb_array_elements(def->'transitions') WITH ORDINALITY AS e(t, ord)), '[]'::jsonb)) END
$$;
UPDATE workflows SET def=jira_issue_event_transitions_remap(def), draft_def=jira_issue_event_transitions_remap(draft_def)
WHERE def::text LIKE '%customIssueEventId%' OR draft_def::text LIKE '%customIssueEventId%';
DROP FUNCTION jira_issue_event_transitions_remap(JSONB);
DROP FUNCTION jira_issue_event_id_remap(BIGINT);

CREATE OR REPLACE FUNCTION provision_workspace_notification_scheme()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE scheme BIGINT;
BEGIN
  INSERT INTO notification_schemes(workspace_id,name,description,is_default)
  VALUES(NEW.id,'Default Notification Scheme','Default notifications for work item activity.',true)
  RETURNING id INTO scheme;
  INSERT INTO notification_scheme_entries(workspace_id,scheme_id,event_id,notification_type)
  SELECT NEW.id,scheme,event_id,recipient
  FROM unnest(ARRAY[1,2,3,4,5,6,7,13]::BIGINT[]) event_id
  CROSS JOIN unnest(ARRAY['CurrentAssignee','Reporter','AllWatchers']::TEXT[]) recipient;
  RETURN NEW;
END;
$$;
