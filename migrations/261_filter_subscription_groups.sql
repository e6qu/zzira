-- Jira's filter subscription can go to a group rather than a list of people,
-- and can be told to send even when the filter matches nothing. A group
-- subscription is one row per (filter, owner, schedule, group), so the unique
-- key that kept one row per schedule grows to include the group.
-- groups.id is a uuid, so the column is one too.
ALTER TABLE filter_subscriptions ADD COLUMN group_id UUID REFERENCES groups(id) ON DELETE CASCADE;
ALTER TABLE filter_subscriptions ADD COLUMN email_when_empty BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE filter_subscriptions DROP CONSTRAINT filter_subscriptions_filter_id_user_id_cron_expression_key;
CREATE UNIQUE INDEX filter_subscriptions_schedule
  ON filter_subscriptions (filter_id, user_id, cron_expression, COALESCE(group_id::text, ''));
