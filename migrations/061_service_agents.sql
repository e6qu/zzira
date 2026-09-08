CREATE TABLE service_desk_agents (
  service_desk_id TEXT NOT NULL REFERENCES service_desks(id) ON DELETE CASCADE,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(service_desk_id,user_id)
);
CREATE INDEX idx_service_desk_agents_user ON service_desk_agents(user_id,service_desk_id);
