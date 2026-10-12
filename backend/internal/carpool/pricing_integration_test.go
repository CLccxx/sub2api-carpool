//go:build integration

package carpool

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCarpoolTotalPricingPostgres(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, TestOrdersEnabled: true})
	legacy, err := s.CreateCar(ctx, 1, testCarInput(3))
	require.NoError(t, err)
	for _, tc := range []struct {
		seats int
		price int64
	}{{2, 70000}, {3, 46667}, {4, 35000}} {
		input := testCarInput(tc.seats)
		require.NoError(t, json.Unmarshal([]byte(`{"total_price_minor":140000,"price_minor":1}`), &input))
		car, err := s.CreateCar(ctx, 1, input)
		require.NoError(t, err)
		require.Equal(t, tc.price, car.PriceMinor, "server must derive the price from the total")
		body, err := json.Marshal(car)
		require.NoError(t, err)
		require.Contains(t, string(body), `"total_price_minor":140000`)
		car, err = s.PublishCar(ctx, 1, car.ID)
		require.NoError(t, err)
		order, err := s.Reserve(ctx, 2, car.ID, car.ID)
		require.NoError(t, err)
		require.Equal(t, tc.price, order.Snapshot.PriceMinor)
		body, err = json.Marshal(order.Snapshot)
		require.NoError(t, err)
		require.Contains(t, string(body), `"total_price_minor":140000`)
	}
	input := legacy.CarInput
	input.Name = "Legacy renamed"
	legacy, err = s.UpdateCar(ctx, 1, legacy.ID, input)
	require.NoError(t, err)
	require.EqualValues(t, 9900, legacy.PriceMinor)
	require.NoError(t, Migrate(ctx, db))
	legacy, err = s.GetCar(ctx, legacy.ID, true)
	require.NoError(t, err)
	require.EqualValues(t, 9900, legacy.PriceMinor)
}

func TestManualTotalPricePreservesReceiptsAndRefunds(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	input := testCarInput(3)
	require.NoError(t, json.Unmarshal([]byte(`{"total_price_minor":140000}`), &input))
	car, err := s.CreateCar(ctx, 1, input)
	require.NoError(t, err)
	input.TotalPriceMinor = new(int64)
	*input.TotalPriceMinor = 150000
	car, err = s.UpdateCar(ctx, 1, car.ID, input)
	require.NoError(t, err)
	require.EqualValues(t, 50000, car.PriceMinor)
	*input.TotalPriceMinor = 140000
	car, err = s.UpdateCar(ctx, 1, car.ID, input)
	require.NoError(t, err)
	require.EqualValues(t, 46667, car.PriceMinor)
	car, err = s.PublishCar(ctx, 1, car.ID)
	require.NoError(t, err)
	receipt := manualInput(2, "total-price-receipt")
	receipt.AmountMinor = 46667
	order, err := s.EnrollManual(ctx, 1, car.ID, "enroll-total", receipt)
	require.NoError(t, err)
	require.EqualValues(t, 46667, order.Payment.AmountMinor)
	require.EqualValues(t, 46667, order.Snapshot.PriceMinor)
	require.EqualValues(t, 140000, *order.Snapshot.TotalPriceMinor)
	*input.TotalPriceMinor = 160000
	_, err = s.UpdateCar(ctx, 1, car.ID, input)
	require.ErrorIs(t, err, ErrMainConflict)
	_, err = s.WithdrawManual(ctx, 1, order.ID, "withdraw-total", ManualWithdrawalInput{Reason: "Test withdrawal", Confirm: true})
	require.NoError(t, err)
	refunded, err := s.RefundManual(ctx, 1, order.ID, "refund-total", ManualRefundInput{AmountMinor: 46667, RefundedAt: time.Now().UTC().Add(-time.Second), ConfirmRefunded: true})
	require.NoError(t, err)
	require.EqualValues(t, 46667, refunded.Payment.Refund.AmountMinor)
	require.EqualValues(t, 140000, *refunded.Snapshot.TotalPriceMinor)
}
