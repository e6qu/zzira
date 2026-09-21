-- An authentication policy says how the people it covers sign in: through the
-- identity provider only, and for how long a session lasts. It is a policy
-- like the others, with members instead of resources, and one of them is the
-- organization's default -- the policy everyone who is in no other one gets.
ALTER TABLE organization_policies DROP CONSTRAINT organization_policies_policy_type_check;
ALTER TABLE organization_policies ADD CONSTRAINT organization_policies_policy_type_check
  CHECK (policy_type IN ('ip-allowlist','data-residency','data-security','authentication-policy'));

CREATE TABLE authentication_policy_members (
  policy_id UUID NOT NULL REFERENCES organization_policies(id) ON DELETE CASCADE,
  user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (policy_id, user_id)
);

-- A person belongs to one authentication policy: two would not say which
-- session duration they get.
CREATE UNIQUE INDEX authentication_policy_members_one_each ON authentication_policy_members (user_id);
