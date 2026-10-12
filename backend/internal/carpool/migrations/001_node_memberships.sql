CREATE TABLE carpool_node_memberships (
    node_id TEXT NOT NULL,
    car_id TEXT NOT NULL,
    issuer TEXT NOT NULL,
    subject TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    status TEXT NOT NULL CHECK (status IN ('active', 'paused', 'revoked')),
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    lease_until TIMESTAMPTZ NOT NULL,
    payload_hash CHAR(64) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_id, car_id, issuer, subject),
    CHECK (valid_until > valid_from),
    CHECK (lease_until <= valid_until)
);
