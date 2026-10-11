-- Keep renderer configuration with the same rule row used by legacy field
-- configurations and newer field association schemes. Empty preserves the
-- existing presentation until an administrator explicitly chooses a renderer.
ALTER TABLE field_configuration_items ADD COLUMN renderer TEXT NOT NULL DEFAULT ''
 CHECK(renderer IN ('','atlassian-wiki-renderer','jira-text-renderer'));
