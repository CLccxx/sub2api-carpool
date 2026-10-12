ALTER TABLE carpool_cars ADD COLUMN total_price_minor BIGINT;

ALTER TABLE carpool_cars ADD CONSTRAINT carpool_car_total_price_valid CHECK (
    total_price_minor IS NULL OR (
        total_price_minor BETWEEN capacity AND 1000000000000
        AND price_minor = (total_price_minor + capacity / 2) / capacity
    )
);
