-- Filter columns name the key column issuekey, as Jira's navigable field does.
UPDATE filters SET columns = array_replace(columns, 'key', 'issuekey') WHERE 'key' = ANY(columns);
