ALTER TABLE carpool_cars
    ADD COLUMN enrollment_mode TEXT NOT NULL DEFAULT 'test' CHECK (enrollment_mode IN ('test','manual')),
    ADD UNIQUE (id,enrollment_mode);
ALTER TABLE carpool_cars DROP CONSTRAINT carpool_cars_status_check;
ALTER TABLE carpool_cars ADD CHECK (status IN ('draft','recruiting','awaiting_deployment','needs_review','closed_unfilled','cancelled'));

ALTER TABLE carpool_orders DROP CONSTRAINT carpool_orders_mode_check;
ALTER TABLE carpool_orders ALTER COLUMN expires_at DROP NOT NULL;
ALTER TABLE carpool_orders ADD CHECK (mode IN ('test','manual')),
    ADD CHECK ((mode='test' AND expires_at IS NOT NULL) OR (mode='manual' AND expires_at IS NULL AND status IN ('confirmed','cancelled'))),
    ADD UNIQUE (id,mode),
    ADD FOREIGN KEY (car_id,mode) REFERENCES carpool_cars(id,enrollment_mode) ON DELETE RESTRICT;

CREATE TABLE carpool_manual_receipts (
    order_id TEXT PRIMARY KEY,
    mode TEXT NOT NULL DEFAULT 'manual' CHECK (mode='manual'),
    amount_minor BIGINT NOT NULL CHECK (amount_minor BETWEEN 1 AND 1000000000000),
    currency TEXT NOT NULL CHECK (currency='CNY'),
    channel TEXT NOT NULL CHECK (channel IN ('alipay','wechat')),
    received_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    recorded_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    reference TEXT CHECK (reference IS NULL OR (reference=btrim(reference) AND length(reference) BETWEEN 1 AND 128)),
    note TEXT NOT NULL DEFAULT '' CHECK (length(note)<=1000),
    UNIQUE (channel,reference),
    UNIQUE (order_id,amount_minor),
    FOREIGN KEY (order_id,mode) REFERENCES carpool_orders(id,mode) ON DELETE RESTRICT
);

CREATE TABLE carpool_manual_refunds (
    order_id TEXT PRIMARY KEY,
    amount_minor BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','refunded')),
    reason TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2000),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    requested_by BIGINT REFERENCES users(id) ON DELETE RESTRICT,
    refunded_at TIMESTAMPTZ,
    recorded_at TIMESTAMPTZ,
    recorded_by BIGINT REFERENCES users(id) ON DELETE RESTRICT,
    reference TEXT CHECK (reference IS NULL OR (reference=btrim(reference) AND length(reference) BETWEEN 1 AND 128)),
    FOREIGN KEY (order_id,amount_minor) REFERENCES carpool_manual_receipts(order_id,amount_minor) ON DELETE RESTRICT,
    CHECK ((status='pending' AND refunded_at IS NULL AND recorded_at IS NULL AND recorded_by IS NULL AND reference IS NULL)
        OR (status='refunded' AND refunded_at IS NOT NULL AND recorded_at IS NOT NULL AND recorded_by IS NOT NULL))
);
CREATE INDEX carpool_manual_refunds_pending ON carpool_manual_refunds(requested_at,order_id) WHERE status='pending';

CREATE TABLE carpool_manual_commands (
    actor_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action IN ('enroll','withdraw','refund')),
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    request_hash CHAR(64) NOT NULL,
    order_id TEXT NOT NULL REFERENCES carpool_manual_receipts(order_id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (actor_id,action,idempotency_key)
);
