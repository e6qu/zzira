ALTER TABLE app_installations ADD COLUMN descriptor_format TEXT NOT NULL DEFAULT 'zzira' CHECK(descriptor_format IN ('zzira','connect'));
ALTER TABLE app_modules ADD COLUMN remote_url TEXT NOT NULL DEFAULT '';
