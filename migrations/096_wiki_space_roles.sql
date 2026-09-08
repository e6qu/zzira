CREATE TABLE wiki_space_roles (
  id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id      TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name              TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 255),
  description       TEXT NOT NULL CHECK(length(description) <= 2000),
  space_permissions TEXT[] NOT NULL DEFAULT '{}',
  UNIQUE(workspace_id,name)
);

CREATE TABLE wiki_space_role_assignments (
  space_id       BIGINT NOT NULL REFERENCES wiki_spaces(id) ON DELETE CASCADE,
  role_id        TEXT NOT NULL,
  principal_type TEXT NOT NULL CHECK(principal_type IN ('USER','GROUP','ACCESS_CLASS')),
  principal_id   TEXT NOT NULL,
  PRIMARY KEY(space_id,role_id,principal_type,principal_id)
);

CREATE INDEX wiki_space_role_assignments_space ON wiki_space_role_assignments(space_id,role_id);
