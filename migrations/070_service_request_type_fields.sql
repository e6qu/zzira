CREATE TABLE service_request_type_fields (
  request_type_id TEXT NOT NULL REFERENCES service_request_types(id) ON DELETE CASCADE,
  field_id        TEXT NOT NULL,
  required        BOOLEAN NOT NULL DEFAULT FALSE,
  help_text       TEXT NOT NULL DEFAULT '',
  position        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (request_type_id,field_id)
);

INSERT INTO service_request_type_fields(request_type_id,field_id,required,help_text,position)
SELECT id,'summary',TRUE,help_text,0 FROM service_request_types
UNION ALL
SELECT id,'description',FALSE,'Describe the request.',1 FROM service_request_types;
