-- The Jira Software DevOps APIs limit how many requests one caller makes to a
-- provider API in a minute. The count for the current minute is kept here so
-- every server shares one limit.
CREATE UNLOGGED TABLE provider_rate_windows (
  workspace_id text NOT NULL,
  principal_id text NOT NULL,
  module text NOT NULL,
  window_start timestamptz NOT NULL,
  count integer NOT NULL,
  PRIMARY KEY (workspace_id, principal_id, module)
);
