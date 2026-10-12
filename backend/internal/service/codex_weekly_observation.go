package service

import "time"

// Keep the tuple atomic: unrelated or partial window updates cannot freshen it.
func setCodexWeeklyObservation(updates map[string]any, normalized *NormalizedCodexLimits, at time.Time) {
	if normalized.Used7dPercent == nil || normalized.Reset7dSeconds == nil {
		return
	}
	updates["codex_7d_observation"] = map[string]any{
		"observed_at": at.Format(time.RFC3339Nano),
		"resets_at":   *codexResetAtRFC3339(at, normalized.Reset7dSeconds),
		"utilization": *normalized.Used7dPercent,
	}
}
