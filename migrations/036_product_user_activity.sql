CREATE TABLE product_user_activity (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  last_active_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (user_id, product_id)
);

CREATE INDEX product_user_activity_product_time
  ON product_user_activity (product_id, last_active_at DESC);
