package carpool

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMainConfigAndCarValidation(t *testing.T) {
	require.NoError(t, (MainConfig{}).Validate(false))
	require.Error(t, (MainConfig{Enabled: true}).Validate(true))
	require.Error(t, (MainConfig{TestOrdersEnabled: true}).Validate(false))
	now := time.Now().UTC()
	valid := CarInput{Name: "Test car", Capacity: 2, PriceMinor: 1000, Currency: "CNY", ServiceDays: 30, RecruitUntil: now.Add(time.Hour), DeliveryHours: 24, RefundPolicy: "Test cancellation only"}
	for _, capacity := range []int{2, 3, 4} {
		input := valid
		input.Capacity = capacity
		require.NoError(t, input.Validate(now))
	}
	for _, change := range []func(*CarInput){
		func(v *CarInput) { v.Capacity = 1 }, func(v *CarInput) { v.Capacity = 5 },
		func(v *CarInput) { v.PriceMinor = 0 }, func(v *CarInput) { v.Currency = "USD" },
		func(v *CarInput) { v.Name = " " }, func(v *CarInput) { v.RefundPolicy = "" },
		func(v *CarInput) { v.ServiceDays = 0 }, func(v *CarInput) { v.DeliveryHours = -1 },
		func(v *CarInput) { v.RecruitUntil = now },
	} {
		input := valid
		change(&input)
		require.Error(t, input.Validate(now))
	}
}
