CREATE TABLE carpool_main_subjects (
    subject_id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE carpool_managed_node_binding (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    main_instance_id UUID NOT NULL,
    node_id TEXT NOT NULL,
    car_id TEXT NOT NULL,
    group_id BIGINT NOT NULL CHECK (group_id>0),
    node_instance_id UUID NOT NULL REFERENCES carpool_control_instance(instance_id),
    UNIQUE(main_instance_id,node_id,car_id,group_id,node_instance_id)
);

CREATE TABLE carpool_managed_members (
    main_instance_id UUID NOT NULL,
    subject_id UUID NOT NULL,
    node_id TEXT NOT NULL,
    car_id TEXT NOT NULL,
    group_id BIGINT NOT NULL,
    node_instance_id UUID NOT NULL,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version>=0),
    status TEXT NOT NULL DEFAULT 'prepared' CHECK (status IN ('prepared','active','paused','revoked')),
    valid_from TIMESTAMPTZ,
    valid_until TIMESTAMPTZ,
    lease_until TIMESTAMPTZ,
    payload_hash TEXT NOT NULL DEFAULT '' CHECK (length(payload_hash) IN (0,64)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(main_instance_id,subject_id),
    FOREIGN KEY(main_instance_id,node_id,car_id,group_id,node_instance_id)
      REFERENCES carpool_managed_node_binding(main_instance_id,node_id,car_id,group_id,node_instance_id),
    CHECK ((status='active' AND version>0 AND valid_from IS NOT NULL AND valid_until IS NOT NULL AND valid_until>valid_from AND lease_until IS NOT NULL AND lease_until<=valid_until)
      OR (status<>'active' AND valid_from IS NULL AND valid_until IS NULL AND lease_until IS NULL)),
    CHECK ((version=0 AND status='prepared' AND payload_hash='') OR (version>0 AND length(payload_hash)=64))
);
