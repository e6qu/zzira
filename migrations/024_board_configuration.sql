ALTER TABLE boards
  ADD COLUMN IF NOT EXISTS quick_filters JSONB NOT NULL DEFAULT '[
    {"id":"qf_my","name":"Only my work","description":"Work assigned to the signed-in user","jql":"assignee = currentUser()","position":0},
    {"id":"qf_unassigned","name":"Unassigned","description":"Work that needs an owner","jql":"assignee IS EMPTY","position":1}
  ]'::jsonb,
  ADD COLUMN IF NOT EXISTS swimlane_strategy TEXT NOT NULL DEFAULT 'none',
  ADD COLUMN IF NOT EXISTS card_fields TEXT[] NOT NULL DEFAULT '{priority,assignee,labels}',
  ADD COLUMN IF NOT EXISTS column_limits JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE boards DROP CONSTRAINT IF EXISTS boards_swimlane_strategy_check;
ALTER TABLE boards
  ADD CONSTRAINT boards_swimlane_strategy_check
  CHECK (swimlane_strategy IN ('none', 'assignee'));
