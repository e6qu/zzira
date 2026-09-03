-- Keep the authorization facts needed to filter immutable sync history after
-- the materialized issue row is deleted.
CREATE TABLE deleted_issue_visibility (
  workspace_id      TEXT NOT NULL,
  issue_id          TEXT NOT NULL,
  project_id        TEXT NOT NULL,
  security_level_id TEXT,
  PRIMARY KEY (workspace_id, issue_id)
);

-- Best-effort backfill for deletions created before this ledger existed. Issue
-- snapshots carry the public key and security level; project keys are unique
-- inside a workspace.
INSERT INTO deleted_issue_visibility (workspace_id, issue_id, project_id, security_level_id)
SELECT d.workspace_id,
       d.entity_id,
       p.id,
       NULLIF(snapshot.payload->'issue'->>'securityLevelId', '')
FROM actions d
JOIN LATERAL (
  SELECT a.payload
  FROM actions a
  WHERE a.workspace_id=d.workspace_id
    AND a.entity_type='issue'
    AND a.entity_id=d.entity_id
    AND a.op='upsert'
    AND a.seq < d.seq
  ORDER BY a.seq DESC
  LIMIT 1
) snapshot ON true
JOIN projects p
  ON p.workspace_id=d.workspace_id
 AND p.key=split_part(snapshot.payload->'issue'->>'key', '-', 1)
WHERE d.entity_type='issue' AND d.op='delete'
ON CONFLICT (workspace_id, issue_id) DO NOTHING;
