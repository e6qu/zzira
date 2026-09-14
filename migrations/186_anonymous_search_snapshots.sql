-- Anonymous callers run enhanced searches too; their snapshots belong to no user.
ALTER TABLE jira_search_snapshots ALTER COLUMN user_id DROP NOT NULL;
