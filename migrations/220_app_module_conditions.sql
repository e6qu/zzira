-- Connect modules may carry conditions that decide who is shown them. They are
-- kept with the module, as validated from its descriptor; null means none.
ALTER TABLE app_modules ADD COLUMN conditions JSONB;
