-- A project's notification sender address was stored and never used. Mail
-- queued about a project's work now carries the address the project chose,
-- and mail that belongs to no project carries none, which reads as the site's.
ALTER TABLE email_outbox ADD COLUMN sender TEXT NOT NULL DEFAULT '';
