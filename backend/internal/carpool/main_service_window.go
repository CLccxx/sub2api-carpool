package carpool

import (
	"context"
	"database/sql"
)

// Call after locking the car. Started service history survives cancellation.
func requirePreService(ctx context.Context, tx *sql.Tx, carID, orderID string) error {
	var served bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_memberships WHERE car_id=$1 AND ($2='' OR order_id=$2)
 AND (service_start IS NOT NULL OR service_end IS NOT NULL))
 OR ($2='' AND EXISTS(SELECT 1 FROM carpool_cars WHERE id=$1 AND (service_start IS NOT NULL OR status IN ('active','paused','completed'))))`, carID, orderID).Scan(&served)
	if err != nil {
		return err
	}
	if served {
		return ErrMainConflict
	}
	return nil
}
