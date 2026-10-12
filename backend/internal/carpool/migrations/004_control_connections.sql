CREATE TABLE carpool_control_instance (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    kind TEXT NOT NULL CHECK (kind IN ('main','node')),
    instance_id UUID NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE carpool_node_connections (
    node_id TEXT PRIMARY KEY REFERENCES carpool_nodes(id) ON DELETE RESTRICT,
    origin TEXT NOT NULL CHECK (length(origin) BETWEEN 1 AND 512),
    expected_instance_id UUID NOT NULL,
    credential_ciphertext TEXT NOT NULL CHECK (length(credential_ciphertext) BETWEEN 1 AND 6000),
    generation BIGINT NOT NULL CHECK (generation > 0),
    status TEXT NOT NULL CHECK (status IN ('unverified','ready','error','disabled')),
    identity_verified BOOLEAN NOT NULL DEFAULT FALSE,
    protocol_version INTEGER NOT NULL DEFAULT 1 CHECK (protocol_version=1),
    check_operation TEXT NOT NULL DEFAULT '',
    last_error_code TEXT NOT NULL DEFAULT '',
    verified_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((status='ready') = (verified_at IS NOT NULL)),
    CHECK (status<>'ready' OR identity_verified)
);
