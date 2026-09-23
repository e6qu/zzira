-- A plan's parents carried whatever dates and estimate somebody typed on
-- them, which for an epic is usually nothing at all, while the work under
-- them said exactly when it runs and how big it is. Rolling up is how a plan
-- reads that: the parent takes the span of its children and the sum of their
-- estimates. It is part of how a view is read, so a saved view keeps it.
ALTER TABLE plan_views ADD COLUMN rollup BOOLEAN NOT NULL DEFAULT FALSE;
