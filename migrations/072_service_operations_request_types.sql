INSERT INTO service_request_type_groups(service_desk_id,id,name,position)
SELECT id,'problems','Problems',2 FROM service_desks
UNION ALL
SELECT id,'changes','Changes',3 FROM service_desks
ON CONFLICT DO NOTHING;

INSERT INTO service_request_types(service_desk_id,name,description,help_text,issue_type_id,group_ids)
SELECT id,'Investigate a problem','Investigate the underlying cause of recurring incidents.','Describe the affected service, related incidents, and known symptoms.','it_task',ARRAY['problems'] FROM service_desks
UNION ALL
SELECT id,'Request a change','Plan, assess, approve, and track a service change.','Describe the change, expected impact, implementation plan, and rollback plan.','it_task',ARRAY['changes'] FROM service_desks
ON CONFLICT(service_desk_id,name) DO NOTHING;

INSERT INTO service_request_type_fields(request_type_id,field_id,required,help_text,position)
SELECT id,'summary',TRUE,help_text,0 FROM service_request_types
WHERE name IN ('Investigate a problem','Request a change')
UNION ALL
SELECT id,'description',TRUE,'Include impact, related work, and the intended outcome.',1 FROM service_request_types
WHERE name IN ('Investigate a problem','Request a change')
ON CONFLICT(request_type_id,field_id) DO NOTHING;
