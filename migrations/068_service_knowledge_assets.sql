CREATE TABLE service_request_type_groups (
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  id              TEXT NOT NULL,
  name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  position        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (service_desk_id,id),
  UNIQUE (service_desk_id,name)
);

INSERT INTO service_request_type_groups(service_desk_id,id,name,position)
SELECT DISTINCT rt.service_desk_id,g.id,
  CASE g.id WHEN 'help' THEN 'Help and support' WHEN 'incidents' THEN 'Incidents'
       ELSE initcap(replace(g.id,'_',' ')) END,
  CASE g.id WHEN 'help' THEN 0 WHEN 'incidents' THEN 1 ELSE 100 END
FROM service_request_types rt CROSS JOIN LATERAL unnest(rt.group_ids) AS g(id)
ON CONFLICT DO NOTHING;

CREATE TABLE service_desk_knowledge_bases (
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  space_id        BIGINT NOT NULL REFERENCES wiki_spaces(id) ON DELETE CASCADE,
  linked_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (service_desk_id,space_id)
);

CREATE TABLE service_assets_workspaces (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id TEXT NOT NULL UNIQUE REFERENCES workspaces(id) ON DELETE CASCADE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO service_assets_workspaces(workspace_id)
SELECT DISTINCT workspace_id FROM service_desks
ON CONFLICT DO NOTHING;
