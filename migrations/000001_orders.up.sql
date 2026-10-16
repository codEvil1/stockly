CREATE TABLE orders (
  id CHAR(36) PRIMARY KEY,
  customer_id VARCHAR(36) NOT NULL,
  status VARCHAR(20) NOT NULL,
  total_cents BIGINT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  INDEX idx_orders_customer (customer_id, created_at)
);

CREATE TABLE order_items (
  order_id CHAR(36) NOT NULL,
  sku VARCHAR(50) NOT NULL,
  quantity INT NOT NULL,
  price_cents BIGINT NOT NULL,
  PRIMARY KEY (order_id, sku),
  FOREIGN KEY (order_id) REFERENCES orders(id)
);

CREATE TABLE outbox (
  id CHAR(36) PRIMARY KEY,
  aggregate_id CHAR(36) NOT NULL,
  type VARCHAR(100) NOT NULL,
  payload JSON NOT NULL,
  created_at DATETIME(6) NOT NULL,
  published_at DATETIME(6) NULL,
  INDEX idx_outbox_pending (published_at, created_at)
);
