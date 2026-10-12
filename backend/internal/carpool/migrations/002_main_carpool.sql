CREATE TABLE carpool_cars (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 160),
    capacity INTEGER NOT NULL CHECK (capacity BETWEEN 2 AND 4),
    price_minor BIGINT NOT NULL CHECK (price_minor BETWEEN 1 AND 1000000000000),
    currency TEXT NOT NULL CHECK (currency = 'CNY'),
    service_days INTEGER NOT NULL CHECK (service_days BETWEEN 1 AND 3650),
    recruit_until TIMESTAMPTZ NOT NULL,
    delivery_hours INTEGER NOT NULL CHECK (delivery_hours BETWEEN 1 AND 8760),
    refund_policy TEXT NOT NULL CHECK (length(btrim(refund_policy)) BETWEEN 1 AND 4000),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','recruiting','awaiting_deployment','closed_unfilled','cancelled')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMPTZ,
    full_at TIMESTAMPTZ,
    delivery_due TIMESTAMPTZ,
    cancel_reason TEXT,
    UNIQUE (id,capacity),
    CHECK ((full_at IS NULL) = (delivery_due IS NULL))
);

CREATE TABLE carpool_seats (
    car_id TEXT NOT NULL,
    seat_no INTEGER NOT NULL,
    capacity INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'available' CHECK (status IN ('available','held','confirmed')),
    user_id BIGINT REFERENCES users(id) ON DELETE RESTRICT,
    order_id TEXT,
    held_until TIMESTAMPTZ,
    PRIMARY KEY (car_id,seat_no),
    FOREIGN KEY (car_id,capacity) REFERENCES carpool_cars(id,capacity) ON DELETE RESTRICT,
    CHECK (seat_no BETWEEN 1 AND capacity),
    CHECK ((status='available' AND user_id IS NULL AND order_id IS NULL AND held_until IS NULL)
        OR (status='held' AND user_id IS NOT NULL AND order_id IS NOT NULL AND held_until IS NOT NULL)
        OR (status='confirmed' AND user_id IS NOT NULL AND order_id IS NOT NULL AND held_until IS NULL))
);
CREATE UNIQUE INDEX carpool_one_live_seat_per_user ON carpool_seats(car_id,user_id) WHERE status<>'available';

CREATE TABLE carpool_orders (
    id TEXT PRIMARY KEY,
    car_id TEXT NOT NULL REFERENCES carpool_cars(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    seat_no INTEGER NOT NULL,
    mode TEXT NOT NULL CHECK (mode='test'),
    status TEXT NOT NULL CHECK (status IN ('pending','confirmed','expired','cancelled')),
    snapshot JSONB NOT NULL CHECK (jsonb_typeof(snapshot)='object'),
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    request_hash CHAR(64) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    confirmed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id,idempotency_key),
    UNIQUE (id,car_id,user_id,seat_no),
    FOREIGN KEY (car_id,seat_no) REFERENCES carpool_seats(car_id,seat_no) ON DELETE RESTRICT
);
ALTER TABLE carpool_seats ADD CONSTRAINT carpool_seat_order_owner
    FOREIGN KEY (order_id,car_id,user_id,seat_no) REFERENCES carpool_orders(id,car_id,user_id,seat_no) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX carpool_orders_user_history ON carpool_orders(user_id,created_at DESC,id);
CREATE INDEX carpool_orders_pending_expiry ON carpool_orders(expires_at,car_id) WHERE status='pending';

CREATE TABLE carpool_memberships (
    id TEXT PRIMARY KEY,
    car_id TEXT NOT NULL,
    user_id BIGINT NOT NULL,
    seat_no INTEGER NOT NULL,
    order_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('waiting_deployment','cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    service_start TIMESTAMPTZ CHECK (service_start IS NULL),
    service_end TIMESTAMPTZ CHECK (service_end IS NULL),
    FOREIGN KEY (order_id,car_id,user_id,seat_no) REFERENCES carpool_orders(id,car_id,user_id,seat_no) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX carpool_one_live_member_per_user ON carpool_memberships(car_id,user_id) WHERE status='waiting_deployment';
CREATE UNIQUE INDEX carpool_one_live_member_per_seat ON carpool_memberships(car_id,seat_no) WHERE status='waiting_deployment';

CREATE TABLE carpool_nodes (
    id TEXT PRIMARY KEY,
    car_id TEXT NOT NULL UNIQUE REFERENCES carpool_cars(id) ON DELETE RESTRICT,
    hostname TEXT NOT NULL UNIQUE CHECK (length(hostname) BETWEEN 1 AND 253),
    label TEXT NOT NULL CHECK (length(label)<=160),
    status TEXT NOT NULL CHECK (status='registered'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE carpool_events (
    id TEXT PRIMARY KEY,
    car_id TEXT NOT NULL REFERENCES carpool_cars(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    actor_id BIGINT REFERENCES users(id) ON DELETE RESTRICT,
    order_id TEXT REFERENCES carpool_orders(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(car_id,version,event_type)
);
CREATE INDEX carpool_cars_recruitment ON carpool_cars(recruit_until,id) WHERE status='recruiting';
