ALTER TABLE orders
  DROP INDEX uq_orders_idempotency_key,
  DROP COLUMN idempotency_key;
