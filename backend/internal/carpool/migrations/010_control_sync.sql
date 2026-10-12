CREATE TABLE carpool_control_grants (
    node_id TEXT NOT NULL,
    subject_id UUID NOT NULL,
    membership_id TEXT NOT NULL REFERENCES carpool_memberships(id) ON DELETE RESTRICT,
    generation BIGINT NOT NULL CHECK (generation>0),
    version BIGINT NOT NULL CHECK (version>0),
    operation_id UUID NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('prepared','active','paused','revoked')),
    valid_from TIMESTAMPTZ,
    valid_until TIMESTAMPTZ,
    lease_until TIMESTAMPTZ,
    payload_hash TEXT NOT NULL CHECK (length(payload_hash)=64),
    delivery TEXT NOT NULL DEFAULT 'pending' CHECK (delivery IN ('pending','leased','synced','error')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts>=0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claim_id UUID,
    claim_until TIMESTAMPTZ,
    ack_version BIGINT NOT NULL DEFAULT 0 CHECK (ack_version>=0 AND ack_version<=version),
    ack_generation BIGINT NOT NULL DEFAULT 0 CHECK (ack_generation>=0 AND ack_generation<=generation),
    synced_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (length(last_error_code)<=100),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(node_id,subject_id),
    FOREIGN KEY(node_id,subject_id) REFERENCES carpool_main_node_members(node_id,subject_id) ON DELETE RESTRICT,
    CHECK ((delivery='leased' AND claim_id IS NOT NULL AND claim_until IS NOT NULL)
       OR (delivery<>'leased' AND claim_id IS NULL AND claim_until IS NULL)),
    CHECK (delivery<>'synced' OR (ack_version=version AND ack_generation=generation AND synced_at IS NOT NULL)),
    CHECK ((status='active' AND valid_from IS NOT NULL AND valid_until IS NOT NULL AND valid_until>valid_from
       AND lease_until IS NOT NULL AND lease_until>valid_from AND lease_until<=valid_until)
       OR (status<>'active' AND valid_from IS NULL AND valid_until IS NULL AND lease_until IS NULL))
);
CREATE INDEX carpool_control_grants_due ON carpool_control_grants(available_at, node_id)
    WHERE delivery IN ('pending','leased');
