CREATE TABLE organization_policies (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  policy_type TEXT NOT NULL CHECK (policy_type IN ('ip-allowlist','data-residency','data-security')),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
  status TEXT NOT NULL DEFAULT 'disabled' CHECK (status IN ('enabled','disabled')),
  rule JSONB NOT NULL DEFAULT '{"in":[]}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (organization_id, name)
);

CREATE TABLE organization_policy_resources (
  policy_id UUID NOT NULL REFERENCES organization_policies(id) ON DELETE CASCADE,
  resource_id TEXT NOT NULL,
  application_status TEXT NOT NULL DEFAULT 'applied'
    CHECK (application_status IN ('applying','removing','applied','failed','scheduled')),
  meta JSONB NOT NULL DEFAULT '{}',
  links JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (policy_id, resource_id)
);

CREATE INDEX organization_policies_lookup
  ON organization_policies (organization_id, policy_type, name);
