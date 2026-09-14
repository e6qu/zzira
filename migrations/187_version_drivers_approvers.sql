-- A release has a driver, the person responsible for it, and approvers who
-- approve or decline it before it ships.
ALTER TABLE project_versions ADD COLUMN driver_account_id TEXT REFERENCES users(id) ON DELETE SET NULL;

CREATE TABLE project_version_approvers (
  version_id     TEXT NOT NULL REFERENCES project_versions(id) ON DELETE CASCADE,
  account_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status         TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','APPROVED','DECLINED')),
  description    TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
  decline_reason TEXT NOT NULL DEFAULT '' CHECK (length(decline_reason) <= 1000),
  added_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (version_id, account_id)
);
