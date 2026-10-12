CREATE TABLE carpool_main_node_members (
    node_id TEXT NOT NULL REFERENCES carpool_nodes(id) ON DELETE RESTRICT,
    subject_id UUID NOT NULL REFERENCES carpool_main_subjects(subject_id) ON DELETE RESTRICT,
    remote_user_id BIGINT,
    remote_group_id BIGINT,
    provisioned_instance_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(node_id,subject_id),
    UNIQUE(node_id,remote_user_id),
    CHECK ((remote_user_id IS NULL AND remote_group_id IS NULL AND provisioned_instance_id IS NULL)
      OR (remote_user_id IS NOT NULL AND remote_user_id>0 AND remote_group_id IS NOT NULL AND remote_group_id>0 AND provisioned_instance_id IS NOT NULL))
);

CREATE TABLE carpool_main_key_commands (
    actor_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    operation_id UUID NOT NULL,
    car_id TEXT NOT NULL REFERENCES carpool_cars(id) ON DELETE RESTRICT,
    membership_id TEXT NOT NULL REFERENCES carpool_memberships(id) ON DELETE RESTRICT,
    node_id TEXT NOT NULL,
    subject_id UUID NOT NULL,
    request_hash TEXT NOT NULL CHECK (length(request_hash)=64),
    remote_operation_id UUID NOT NULL UNIQUE,
    remote_key_id BIGINT CHECK (remote_key_id>0),
    generation BIGINT NOT NULL CHECK (generation>0),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','succeeded','gone')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(actor_id,operation_id),
    FOREIGN KEY(node_id,subject_id) REFERENCES carpool_main_node_members(node_id,subject_id) ON DELETE RESTRICT,
    CHECK (status<>'succeeded' OR remote_key_id IS NOT NULL)
);
