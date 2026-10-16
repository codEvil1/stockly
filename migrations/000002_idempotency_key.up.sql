-- Chave de idempotência enviada pelo cliente no header Idempotency-Key.
-- NULL para pedidos criados sem chave; a UNIQUE aceita vários NULLs.
ALTER TABLE orders
  ADD COLUMN idempotency_key VARCHAR(64) NULL,
  ADD UNIQUE INDEX uq_orders_idempotency_key (idempotency_key);
