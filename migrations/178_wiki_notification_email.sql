-- Confluence notifications are emailed as well as shown in the inbox. Only
-- notifications written from now on are sent; earlier ones stay inbox-only.
ALTER TABLE notifications ADD COLUMN email_state text NOT NULL DEFAULT 'none'
  CHECK (email_state IN ('none', 'pending', 'queued', 'skipped'));
CREATE INDEX notifications_email_pending ON notifications (created_at, id) WHERE email_state = 'pending';
