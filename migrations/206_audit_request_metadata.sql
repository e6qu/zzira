-- Organization audit events record the client address and user agent of the
-- request that caused them, which the store sets on its connection.
CREATE FUNCTION organization_audit_request_metadata() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.detail := COALESCE(NEW.detail, '{}'::jsonb);
  IF NULLIF(current_setting('zzira.request_ip', true), '') IS NOT NULL AND NEW.detail->>'ip' IS NULL THEN
    NEW.detail := NEW.detail || jsonb_build_object('ip', current_setting('zzira.request_ip', true));
  END IF;
  IF NULLIF(current_setting('zzira.user_agent', true), '') IS NOT NULL AND NEW.detail->>'userAgent' IS NULL THEN
    NEW.detail := NEW.detail || jsonb_build_object('userAgent', current_setting('zzira.user_agent', true));
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER organization_audit_request_metadata BEFORE INSERT ON organization_audit_events
  FOR EACH ROW EXECUTE FUNCTION organization_audit_request_metadata();
