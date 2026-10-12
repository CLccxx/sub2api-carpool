package carpool

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCarInputAcceptsTotalWithoutClientSeatPrice(t *testing.T) {
	for _, capacity := range []int{2, 3, 4} {
		input := CarInput{Name: "Total-priced car", Capacity: capacity, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().Add(time.Hour), DeliveryHours: 24, RefundPolicy: "Local test"}
		require.NoError(t, json.Unmarshal([]byte(`{"total_price_minor":140000}`), &input))
		require.NoError(t, input.Validate(time.Now()))
	}
}

func TestCarInputRejectsInvalidTotalEvenWithValidSeatPrice(t *testing.T) {
	for _, raw := range []string{`0`, `-1`, `1`, `1000000000001`} {
		input := CarInput{Name: "Total-priced car", Capacity: 4, PriceMinor: 35000, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().Add(time.Hour), DeliveryHours: 24, RefundPolicy: "Local test"}
		require.NoError(t, json.Unmarshal([]byte(`{"total_price_minor":`+raw+`}`), &input))
		require.ErrorIs(t, input.Validate(time.Now()), ErrMainInvalid)
	}
}
