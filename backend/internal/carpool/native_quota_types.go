package carpool

import (
	"math"
	"time"
)

const nativeWeek = 7 * 24 * time.Hour
const nativeObservationMaxAge = 15 * time.Minute
const nativeCycleMaxDrift = 5 * time.Minute

// QuotaObservation is supplied by B's native account reader, never by an HTTP client.
type QuotaObservation struct {
	CapturedAt     time.Time
	ObservedAt     time.Time
	ResetsAt       time.Time
	Utilization    float64
	EstimatedTotal *float64
}

func (s QuotaObservation) canonical() QuotaObservation {
	s.CapturedAt = s.CapturedAt.UTC().Truncate(time.Microsecond)
	s.ObservedAt = s.ObservedAt.UTC().Truncate(time.Microsecond)
	s.ResetsAt = s.ResetsAt.UTC().Truncate(time.Microsecond)
	return s
}

func nativeWeeklyLimit(capacity int, estimate *float64) (float64, error) {
	if capacity < 2 || capacity > 4 {
		return 0, ErrMainInvalid
	}
	if estimate == nil {
		return 1800 / float64(capacity), nil
	}
	if math.IsNaN(*estimate) || math.IsInf(*estimate, 0) || *estimate <= 100 || *estimate > 1e12 {
		return 0, ErrMainInvalid
	}
	limit := math.Round((*estimate-100)/float64(capacity)*1e8) / 1e8
	if limit <= 0 {
		return 0, ErrMainInvalid
	}
	return limit, nil
}

func (s QuotaObservation) validate(now time.Time) error {
	if s.CapturedAt.IsZero() || s.ObservedAt.IsZero() || s.ResetsAt.IsZero() ||
		s.CapturedAt.After(now.Add(time.Minute)) || s.ObservedAt.After(s.CapturedAt.Add(time.Minute)) ||
		now.Sub(s.ObservedAt) > nativeObservationMaxAge || !s.ResetsAt.After(now) ||
		s.ResetsAt.After(s.ObservedAt.Add(nativeWeek+time.Minute)) ||
		math.IsNaN(s.Utilization) || math.IsInf(s.Utilization, 0) || s.Utilization < 0 {
		return ErrMainInvalid
	}
	if s.EstimatedTotal != nil {
		_, err := nativeWeeklyLimit(4, s.EstimatedTotal)
		return err
	}
	return nil
}

func nativeCycleChanged(oldReset, oldObserved *time.Time, s QuotaObservation) (bool, error) {
	if oldReset == nil {
		return true, nil
	}
	delta := s.ResetsAt.Sub(*oldReset)
	weeks := time.Duration(math.Round(float64(delta) / float64(nativeWeek)))
	drift := delta - weeks*nativeWeek
	// Small timestamp corrections are not cycle identities. Reject ambiguous jumps.
	if weeks < 0 || drift < -nativeCycleMaxDrift || drift > nativeCycleMaxDrift {
		return false, ErrMainConflict
	}
	if weeks == 0 {
		return false, nil
	}
	if oldObserved == nil || s.ResetsAt.Add(-nativeWeek).After(s.ObservedAt) {
		return false, ErrMainConflict
	}
	return true, nil
}

type NativeQuotaStatus struct {
	WeeklyLimitUSD float64    `json:"weekly_limit_usd"`
	Utilization    *float64   `json:"utilization"`
	EstimatedTotal *float64   `json:"estimated_total"`
	ObservedAt     *time.Time `json:"observed_at"`
	ResetsAt       *time.Time `json:"resets_at"`
	LastCheckedAt  *time.Time `json:"last_checked_at"`
	QuotaUpdatedAt *time.Time `json:"quota_updated_at"`
	CycleSyncedAt  *time.Time `json:"cycle_synced_at"`
	State          string     `json:"state"`
	LastErrorCode  string     `json:"last_error_code"`
	CachePending   bool       `json:"cache_pending"`
}
