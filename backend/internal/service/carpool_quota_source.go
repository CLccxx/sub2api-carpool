package service

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
)

func (s *AccountUsageService) readCarpoolQuota(ctx context.Context, id int64) (carpool.QuotaObservation, error) {
	captured := time.Now()
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return carpool.QuotaObservation{}, carpool.ErrUnavailable
	}
	usage, err := s.GetUsageForAccount(ctx, account)
	if err != nil || usage == nil {
		return carpool.QuotaObservation{}, carpool.ErrUnavailable
	}
	if account.Platform == PlatformOpenAI {
		weekly, err := readCodexWeeklyObservation(account.Extra)
		if err != nil {
			return carpool.QuotaObservation{}, err
		}
		// Native UI fields may be partial or synthesize zero after expiration.
		// Read cost for this coherent window, not whichever window the UI rendered.
		copyUsage := *usage
		copyUsage.SevenDay = &UsageProgress{Utilization: *weekly.Utilization, ResetsAt: &weekly.ResetsAt}
		usage = &copyUsage
	}
	var acquired *time.Time
	if account.Platform == PlatformAnthropic {
		if usage.Source == "passive" {
			acquired = usage.UpdatedAt
		} else if s.cache != nil {
			if value, ok := s.cache.apiCache.Load(id); ok {
				if cached, ok := value.(*apiUsageCache); ok && cached.err == nil && cached.response != nil {
					timestamp := cached.timestamp
					acquired = &timestamp
				}
			}
		}
	}
	// Some native platforms do not attach seven-day statistics to the UI response.
	if usage.SevenDay != nil && usage.SevenDay.ResetsAt != nil && usage.SevenDay.Utilization > 0 && usage.SevenDay.WindowStats == nil {
		stats, statsErr := s.GetAccountWindowStats(ctx, id, usage.SevenDay.ResetsAt.Add(-7*24*time.Hour))
		if statsErr != nil || stats == nil {
			return carpool.QuotaObservation{}, carpool.ErrUnavailable
		}
		usage.SevenDay.WindowStats = windowStatsFromAccountStats(stats)
	}
	return carpoolQuotaObservation(account, usage, captured, acquired)
}

func carpoolQuotaObservation(account *Account, usage *UsageInfo, captured time.Time, acquired *time.Time) (carpool.QuotaObservation, error) {
	if account == nil || account.Type != AccountTypeOAuth || usage == nil || usage.Error != "" || usage.SevenDay == nil || usage.SevenDay.ResetsAt == nil {
		return carpool.QuotaObservation{}, carpool.ErrMainInvalid
	}
	var observed time.Time
	switch account.Platform {
	case PlatformOpenAI:
		weekly, err := readCodexWeeklyObservation(account.Extra)
		if err != nil || !weekly.ResetsAt.Equal(*usage.SevenDay.ResetsAt) {
			return carpool.QuotaObservation{}, carpool.ErrMainInvalid
		}
		observed = weekly.ObservedAt
		copyUsage := *usage
		copyWindow := *usage.SevenDay
		copyWindow.Utilization = *weekly.Utilization
		copyUsage.SevenDay = &copyWindow
		usage = &copyUsage
	case PlatformAnthropic:
		if acquired == nil || acquired.IsZero() {
			return carpool.QuotaObservation{}, carpool.ErrMainInvalid
		}
		observed = *acquired
	default:
		return carpool.QuotaObservation{}, carpool.ErrMainInvalid
	}
	window := usage.SevenDay
	sample := carpool.QuotaObservation{CapturedAt: captured, ObservedAt: observed, ResetsAt: *window.ResetsAt, Utilization: window.Utilization}
	if window.Utilization > 0 && window.WindowStats != nil {
		cost := window.WindowStats.Cost
		if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
			return carpool.QuotaObservation{}, carpool.ErrMainInvalid
		}
		if cost > 0 {
			estimate := cost * 100 / window.Utilization
			sample.EstimatedTotal = &estimate
		}
	}
	return sample, nil
}

type codexWeeklyObservation struct {
	ObservedAt  time.Time `json:"observed_at"`
	ResetsAt    time.Time `json:"resets_at"`
	Utilization *float64  `json:"utilization"`
}

func readCodexWeeklyObservation(extra map[string]any) (codexWeeklyObservation, error) {
	var sample codexWeeklyObservation
	encoded, err := json.Marshal(extra["codex_7d_observation"])
	if err != nil || json.Unmarshal(encoded, &sample) != nil || sample.ObservedAt.IsZero() || sample.ResetsAt.IsZero() || sample.Utilization == nil || math.IsNaN(*sample.Utilization) || math.IsInf(*sample.Utilization, 0) || *sample.Utilization < 0 {
		return sample, carpool.ErrMainInvalid
	}
	return sample, nil
}
