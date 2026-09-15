-- The largest attachment a site accepts, in bytes, as Jira administrators set
-- under attachment settings. Sites keep the 32 MiB limit they had.
ALTER TABLE jira_site_configuration
  ADD COLUMN attachment_upload_limit BIGINT NOT NULL DEFAULT 33554432
  CHECK (attachment_upload_limit BETWEEN 1 AND 1073741824);
