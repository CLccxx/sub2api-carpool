ALTER TABLE carpool_cars
    ADD COLUMN service_start TIMESTAMPTZ,
    ADD COLUMN service_end TIMESTAMPTZ;
ALTER TABLE carpool_cars DROP CONSTRAINT carpool_cars_status_check;
ALTER TABLE carpool_cars ADD CHECK (status IN ('draft','recruiting','awaiting_deployment','needs_review','closed_unfilled','cancelled','active','paused','completed'));
ALTER TABLE carpool_cars ADD CONSTRAINT carpool_car_service_window CHECK (
    (service_start IS NULL AND service_end IS NULL AND status IN ('draft','recruiting','awaiting_deployment','needs_review','closed_unfilled','cancelled'))
    OR (service_start IS NOT NULL AND service_end IS NOT NULL AND service_end>service_start AND status IN ('active','paused','completed','cancelled'))
);

ALTER TABLE carpool_memberships DROP CONSTRAINT carpool_memberships_status_check;
ALTER TABLE carpool_memberships DROP CONSTRAINT carpool_memberships_service_start_check;
ALTER TABLE carpool_memberships DROP CONSTRAINT carpool_memberships_service_end_check;
ALTER TABLE carpool_memberships ADD CHECK (status IN ('waiting_deployment','active','paused','revoked','expired','cancelled'));
ALTER TABLE carpool_memberships ADD CONSTRAINT carpool_member_service_window CHECK (
    (service_start IS NULL AND service_end IS NULL AND status IN ('waiting_deployment','cancelled','revoked'))
    OR (service_start IS NOT NULL AND service_end IS NOT NULL AND service_end>service_start AND status IN ('active','paused','revoked','expired','cancelled'))
);
DROP INDEX carpool_one_live_member_per_user;
DROP INDEX carpool_one_live_member_per_seat;
CREATE UNIQUE INDEX carpool_one_live_member_per_user ON carpool_memberships(car_id,user_id) WHERE status IN ('waiting_deployment','active','paused');
CREATE UNIQUE INDEX carpool_one_live_member_per_seat ON carpool_memberships(car_id,seat_no) WHERE status IN ('waiting_deployment','active','paused');
