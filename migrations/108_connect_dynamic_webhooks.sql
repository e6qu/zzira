ALTER TABLE app_webhook_modules ADD COLUMN dynamic BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE app_webhook_modules ADD COLUMN exclude_body BOOLEAN NOT NULL DEFAULT false;
