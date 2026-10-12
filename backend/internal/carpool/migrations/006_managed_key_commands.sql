CREATE TABLE carpool_node_key_commands (
    main_instance_id UUID NOT NULL,
    subject_id UUID NOT NULL,
    operation_id UUID NOT NULL,
    request_hash TEXT NOT NULL CHECK (length(request_hash)=64),
    key_id BIGINT NOT NULL CHECK (key_id>0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(main_instance_id,subject_id,operation_id),
    FOREIGN KEY(main_instance_id,subject_id)
      REFERENCES carpool_managed_members(main_instance_id,subject_id) ON DELETE RESTRICT
);
