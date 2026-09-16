ALTER TABLE report_subscriptions ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';
ALTER TABLE dashboard_subscriptions ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';
