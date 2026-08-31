ALTER TABLE webhook_deliveries
  ADD COLUMN next_attempt_at TIMESTAMPTZ;

CREATE INDEX idx_webhook_deliveries_retry
  ON webhook_deliveries (state, next_attempt_at)
  WHERE state = 'failed';
