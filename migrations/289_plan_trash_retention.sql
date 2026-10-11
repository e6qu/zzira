-- Retention is based on the trash transition, independently of later metadata
-- changes. Legacy trashed plans already record that transition in updated_at.
ALTER TABLE plans ADD COLUMN trashed_at TIMESTAMPTZ;
UPDATE plans SET trashed_at=updated_at WHERE status='Trashed';
CREATE INDEX plans_trash_retention ON plans(trashed_at,workspace_id,id)
  WHERE status='Trashed';

CREATE FUNCTION record_plan_trash_time() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status<>'Trashed' THEN
    NEW.trashed_at=NULL;
  ELSIF TG_OP='INSERT' THEN
    NEW.trashed_at=now();
  ELSIF OLD.status IS DISTINCT FROM NEW.status THEN
    NEW.trashed_at=now();
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER plans_trash_time BEFORE INSERT OR UPDATE OF status ON plans
  FOR EACH ROW EXECUTE FUNCTION record_plan_trash_time();
