-- A policy covers the people in a group as well as the people named on it.
-- A site with a hundred contractors puts the contractors' group under the
-- strict policy once, rather than naming a hundred people and naming the
-- hundred and first when they arrive.
CREATE TABLE authentication_policy_groups (
  policy_id UUID NOT NULL REFERENCES organization_policies(id) ON DELETE CASCADE,
  group_id  UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (policy_id, group_id)
);

-- A group belongs to one authentication policy, for the same reason a person
-- does: two would not say which session duration its members get.
CREATE UNIQUE INDEX authentication_policy_groups_one_each ON authentication_policy_groups (group_id);
