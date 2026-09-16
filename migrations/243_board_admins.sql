-- Jira Software boards carry administrators: the users and groups who can
-- configure the board. A board starts with none, as Jira leaves it, and its
-- project's administrators keep administering it either way.
CREATE TABLE board_admins (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  board_id   TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  admin_type TEXT NOT NULL CHECK (admin_type IN ('user','group')),
  account_id TEXT REFERENCES users(id) ON DELETE CASCADE,
  group_id   UUID REFERENCES groups(id) ON DELETE CASCADE,
  CHECK (
    (admin_type='user'  AND account_id IS NOT NULL AND group_id IS NULL) OR
    (admin_type='group' AND account_id IS NULL     AND group_id IS NOT NULL)
  )
);
CREATE UNIQUE INDEX board_admins_unique
  ON board_admins (board_id, admin_type, COALESCE(account_id,''), COALESCE(group_id::TEXT,''));
CREATE INDEX board_admins_board ON board_admins(board_id,id);
