-- Work item notifications are emailed as HTML with a plain-text alternative.
ALTER TABLE email_outbox ADD COLUMN html_body TEXT NOT NULL DEFAULT '';
