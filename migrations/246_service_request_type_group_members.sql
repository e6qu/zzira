-- Customer request type groups order the portal. Jira Service Management lets
-- a desk administrator arrange the groups, and the request types inside each
-- group, in an arbitrary order for display on the customer portal, so group
-- membership carries a position and stops being an unordered array.
CREATE TABLE service_request_type_group_members (
  service_desk_id TEXT NOT NULL,
  group_id        TEXT NOT NULL,
  request_type_id TEXT NOT NULL REFERENCES service_request_types(id) ON DELETE CASCADE,
  position        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (service_desk_id,group_id,request_type_id),
  FOREIGN KEY (service_desk_id,group_id) REFERENCES service_request_type_groups(service_desk_id,id) ON DELETE CASCADE
);
CREATE INDEX idx_service_request_type_group_members_type ON service_request_type_group_members(request_type_id);

INSERT INTO service_request_type_group_members(service_desk_id,group_id,request_type_id,position)
SELECT rt.service_desk_id,g.id,rt.id,
       row_number() OVER (PARTITION BY rt.service_desk_id,g.id ORDER BY rt.id::bigint)-1
FROM service_request_types rt CROSS JOIN LATERAL unnest(rt.group_ids) AS g(id)
WHERE EXISTS (SELECT 1 FROM service_request_type_groups grp
              WHERE grp.service_desk_id=rt.service_desk_id AND grp.id=g.id)
ON CONFLICT DO NOTHING;

ALTER TABLE service_request_types DROP COLUMN group_ids;
