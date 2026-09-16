-- SLA metrics count time between Jira's start and stop conditions. Existing
-- metrics keep their behaviour: time to first response starts when the issue
-- is created and stops at a comment for customers; time to resolution starts
-- when the issue is created or its resolution is cleared and stops when a
-- resolution is set.
ALTER TABLE service_sla_metrics
  ADD COLUMN start_conditions TEXT[] NOT NULL DEFAULT ARRAY['issue_created'],
  ADD COLUMN stop_conditions TEXT[] NOT NULL DEFAULT ARRAY['resolution_set'],
  ADD CONSTRAINT service_sla_metrics_conditions CHECK (cardinality(start_conditions) > 0 AND cardinality(stop_conditions) > 0);

UPDATE service_sla_metrics SET stop_conditions=ARRAY['comment_for_customers'] WHERE kind='first_response';
UPDATE service_sla_metrics SET start_conditions=ARRAY['issue_created','resolution_cleared'] WHERE kind='resolution';

CREATE OR REPLACE FUNCTION seed_service_desk_slas() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE calendar_key TEXT;
BEGIN
  INSERT INTO service_calendars(service_desk_id) VALUES(NEW.id) RETURNING id INTO calendar_key;
  INSERT INTO service_sla_metrics(service_desk_id,calendar_id,name,kind,goal_millis,position,start_conditions,stop_conditions) VALUES
    (NEW.id,calendar_key,'Time to first response','first_response',14400000,0,ARRAY['issue_created'],ARRAY['comment_for_customers']),
    (NEW.id,calendar_key,'Time to resolution','resolution',28800000,1,ARRAY['issue_created','resolution_cleared'],ARRAY['resolution_set']);
  RETURN NEW;
END $$;
