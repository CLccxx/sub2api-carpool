ALTER TABLE carpool_native_setup
    ADD COLUMN last_read_at TIMESTAMPTZ,
    ADD COLUMN observed_at TIMESTAMPTZ,
    ADD COLUMN resets_at TIMESTAMPTZ,
    ADD COLUMN utilization DOUBLE PRECISION,
    ADD COLUMN estimated_total DOUBLE PRECISION,
    ADD COLUMN cycle_reset_at TIMESTAMPTZ,
    ADD COLUMN cycle_synced_at TIMESTAMPTZ,
    ADD COLUMN quota_updated_at TIMESTAMPTZ,
    ADD COLUMN last_error_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN quota_revision BIGINT NOT NULL DEFAULT 0 CHECK (quota_revision>=0),
    ADD COLUMN cache_ack_revision BIGINT NOT NULL DEFAULT 0 CHECK (cache_ack_revision>=0 AND cache_ack_revision<=quota_revision);
