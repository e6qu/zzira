-- Deleting a request type removes it from its customer requests, which remain,
-- as in Jira Service Management; it no longer fails while requests use it.
ALTER TABLE service_requests ALTER COLUMN request_type_id DROP NOT NULL;
ALTER TABLE service_requests DROP CONSTRAINT service_requests_request_type_id_fkey;
ALTER TABLE service_requests ADD CONSTRAINT service_requests_request_type_id_fkey
  FOREIGN KEY (request_type_id) REFERENCES service_request_types(id) ON DELETE SET NULL;
