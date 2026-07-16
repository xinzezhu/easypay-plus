CREATE TABLE IF NOT EXISTS products (
    id VARCHAR(40) PRIMARY KEY,
    code VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(100) NOT NULL,
    status VARCHAR(16) NOT NULL,
    notify_url VARCHAR(500) NOT NULL,
    return_url VARCHAR(500) NOT NULL DEFAULT '',
    api_secret_enc TEXT NOT NULL,
    notify_secret_enc TEXT NOT NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    disabled_at DATETIME(3) NULL,
    INDEX idx_products_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS payment_orders (
    id VARCHAR(40) PRIMARY KEY,
    product_id VARCHAR(40) NOT NULL,
    product_order_no VARCHAR(100) NOT NULL,
    pay_id VARCHAR(50) NOT NULL UNIQUE,
    epay_order_id VARCHAR(100) NULL UNIQUE,
    pay_type TINYINT UNSIGNED NOT NULL,
    goods_name VARCHAR(50) NOT NULL,
    amount_cents BIGINT UNSIGNED NOT NULL,
    really_amount_cents BIGINT UNSIGNED NULL,
    status VARCHAR(20) NOT NULL,
    pay_url TEXT NOT NULL,
    notify_raw JSON NULL,
    expires_at DATETIME(3) NULL,
    paid_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    CONSTRAINT fk_orders_product FOREIGN KEY (product_id) REFERENCES products(id),
    UNIQUE KEY uq_product_order (product_id, product_order_no),
    INDEX idx_orders_status_created (status, created_at),
    INDEX idx_orders_product_created (product_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS deliveries (
    id VARCHAR(40) PRIMARY KEY,
    order_id VARCHAR(40) NOT NULL,
    product_id VARCHAR(40) NOT NULL,
    event_type VARCHAR(40) NOT NULL,
    endpoint VARCHAR(500) NOT NULL,
    payload JSON NOT NULL,
    status VARCHAR(20) NOT NULL,
    attempt_count INT UNSIGNED NOT NULL DEFAULT 0,
    next_attempt_at DATETIME(3) NOT NULL,
    locked_until DATETIME(3) NULL,
    http_status INT NULL,
    last_error VARCHAR(1000) NOT NULL DEFAULT '',
    delivered_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    CONSTRAINT fk_deliveries_order FOREIGN KEY (order_id) REFERENCES payment_orders(id),
    CONSTRAINT fk_deliveries_product FOREIGN KEY (product_id) REFERENCES products(id),
    UNIQUE KEY uq_order_event (order_id, event_type),
    INDEX idx_deliveries_due (status, next_attempt_at),
    INDEX idx_deliveries_lock (locked_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS api_nonces (
    product_id VARCHAR(40) NOT NULL,
    nonce VARCHAR(100) NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    PRIMARY KEY (product_id, nonce),
    INDEX idx_nonces_expiry (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
