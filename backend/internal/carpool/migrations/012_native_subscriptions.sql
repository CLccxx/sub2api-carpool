ALTER TABLE carpool_managed_members ADD COLUMN native_subscription BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE carpool_control_grants ADD COLUMN native_subscription BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE carpool_native_setup (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    capacity INTEGER NOT NULL CHECK (capacity BETWEEN 2 AND 4),
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    group_id BIGINT NOT NULL CHECK (group_id > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(singleton) REFERENCES carpool_managed_node_binding(singleton)
);

CREATE TABLE carpool_native_preparations (
    node_id TEXT PRIMARY KEY REFERENCES carpool_nodes(id),
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    capacity INTEGER NOT NULL CHECK (capacity BETWEEN 2 AND 4),
    group_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    prepared_at TIMESTAMPTZ,
    CHECK ((group_id IS NULL AND prepared_at IS NULL) OR (group_id > 0 AND prepared_at IS NOT NULL))
);
