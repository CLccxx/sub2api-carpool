CREATE TABLE carpool_main_key_barriers (
    node_id TEXT NOT NULL,
    subject_id UUID NOT NULL,
    remote_key_id BIGINT NOT NULL CHECK (remote_key_id>0),
    reason TEXT NOT NULL CHECK (reason IN ('disabled','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(node_id,subject_id,remote_key_id),
    FOREIGN KEY(node_id,subject_id) REFERENCES carpool_main_node_members(node_id,subject_id) ON DELETE RESTRICT
);
