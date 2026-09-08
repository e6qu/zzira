CREATE TABLE organization_domains (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  claim_type TEXT NOT NULL DEFAULT 'dns' CHECK (claim_type IN ('dns','http')),
  claim_status TEXT NOT NULL DEFAULT 'unverified'
    CHECK (claim_status IN ('verified','deleted','unverified','superseded','missing_token')),
  verification_token TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  verified_at TIMESTAMPTZ,
  UNIQUE (organization_id, name)
);

CREATE INDEX organization_domains_lookup
  ON organization_domains (organization_id, name);
