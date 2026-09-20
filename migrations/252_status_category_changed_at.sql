-- Jira carries the moment a work item's status category last changed, and
-- searches and orders by it. Work that existed before this column keeps the
-- closest thing the log can say: the last status change that crossed a
-- category boundary, or the moment the work was created.
ALTER TABLE issues ADD COLUMN status_category_changed_at TIMESTAMPTZ;

UPDATE issues i SET status_category_changed_at = COALESCE((
  SELECT max(a.created_at)
  FROM actions a
  JOIN statuses moved_from ON moved_from.id = a.payload->'diff'->'status'->>'from'
  JOIN statuses moved_to ON moved_to.id = a.payload->'diff'->'status'->>'to'
  WHERE a.workspace_id = i.workspace_id AND a.entity_type = 'issue' AND a.entity_id = i.id
    AND moved_from.category <> moved_to.category
), i.created_at);

ALTER TABLE issues ALTER COLUMN status_category_changed_at SET NOT NULL;
ALTER TABLE issues ALTER COLUMN status_category_changed_at SET DEFAULT now();

CREATE INDEX idx_issues_status_category_changed ON issues (workspace_id, status_category_changed_at DESC);

-- Four places move a work item's status: an edit or transition, a move, a
-- workflow migration and a workflow scheme change. The date belongs to the
-- status, not to the caller, so the database keeps it rather than each of
-- them remembering to. A move between two statuses of the same category is
-- not a category change and leaves it alone.
CREATE FUNCTION stamp_issue_status_category_change() RETURNS trigger AS $$
BEGIN
  IF NEW.status_id IS DISTINCT FROM OLD.status_id
     AND (SELECT category FROM statuses WHERE id = NEW.status_id)
         IS DISTINCT FROM (SELECT category FROM statuses WHERE id = OLD.status_id) THEN
    NEW.status_category_changed_at := now();
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER stamp_issue_status_category_change_before_update
  BEFORE UPDATE ON issues
  FOR EACH ROW EXECUTE FUNCTION stamp_issue_status_category_change();
