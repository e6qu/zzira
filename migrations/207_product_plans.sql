-- Each product runs on a plan. Free plans cap how many users a product may
-- have, and inviting users needs at least one paid product.
ALTER TABLE products ADD COLUMN plan TEXT NOT NULL DEFAULT 'standard'
  CHECK (plan IN ('free','standard','premium','enterprise'));
