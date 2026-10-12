package carpool

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
)

func (c *ControlClient) MemberSubscription(ctx context.Context, origin, credential string, target ManagedTarget, subject string) (NativeMemberSubscription, error) {
	var result NativeMemberSubscription
	if target.Validate() != nil || !validManagedUUID(subject) {
		return result, ErrMainInvalid
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodGet, controlPrefix+"/members/"+subject+"/subscription", nil, &target)
	if err != nil {
		return result, err
	}
	if !nativeQuotaFieldsPresent(data, "weekly_used_usd", "weekly_limit_usd", "weekly_remaining_usd", "account", "server_time") || json.Unmarshal(data, &result) != nil || !result.valid() {
		return NativeMemberSubscription{}, ErrControlProtocol
	}
	result.APIBaseURL = origin
	if result.Platform == "openai" {
		result.APIBaseURL += "/v1"
	}
	return result, nil
}

func (c *ControlClient) NativeQuota(ctx context.Context, origin, credential string, target ManagedTarget, force bool) (NativeQuotaStatus, error) {
	var result NativeQuotaStatus
	if err := target.Validate(); err != nil {
		return result, err
	}
	method, path := http.MethodGet, controlPrefix+"/subscription/quota"
	var body io.Reader
	if force {
		method = http.MethodPost
		path += "/sync"
		body = strings.NewReader("{}")
	}
	data, err := c.requestBound(ctx, origin, credential, method, path, body, &target)
	if err != nil {
		return result, err
	}
	if json.Unmarshal(data, &result) != nil || !result.valid() {
		return NativeQuotaStatus{}, ErrControlProtocol
	}
	return result, nil
}

func nativeQuotaFieldsPresent(data []byte, fields ...string) bool {
	var values map[string]json.RawMessage
	if json.Unmarshal(data, &values) != nil {
		return false
	}
	for _, field := range fields {
		value, exists := values[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	return true
}

func (s NativeMemberSubscription) valid() bool {
	if s.GroupName == "" || (s.Platform != "openai" && s.Platform != "anthropic") || s.StartsAt.IsZero() || s.ServerTime.IsZero() || !s.ExpiresAt.After(s.StartsAt) || s.WeeklyUsedUSD < 0 || math.IsNaN(s.WeeklyUsedUSD) || math.IsInf(s.WeeklyUsedUSD, 0) || !s.Account.valid() || s.WeeklyLimitUSD != s.Account.WeeklyLimitUSD {
		return false
	}
	if s.Status != "active" && s.Status != "expired" && s.Status != "suspended" {
		return false
	}
	return !math.IsNaN(s.WeeklyRemainingUSD) && !math.IsInf(s.WeeklyRemainingUSD, 0) && math.Abs(s.WeeklyRemainingUSD-math.Max(0, s.WeeklyLimitUSD-s.WeeklyUsedUSD)) < 1e-7
}

func (s NativeQuotaStatus) valid() bool {
	if s.WeeklyLimitUSD <= 0 || math.IsNaN(s.WeeklyLimitUSD) || math.IsInf(s.WeeklyLimitUSD, 0) {
		return false
	}
	if s.Utilization != nil && (*s.Utilization < 0 || math.IsNaN(*s.Utilization) || math.IsInf(*s.Utilization, 0)) {
		return false
	}
	if s.EstimatedTotal != nil && (*s.EstimatedTotal <= 100 || math.IsNaN(*s.EstimatedTotal) || math.IsInf(*s.EstimatedTotal, 0)) {
		return false
	}
	switch s.State {
	case "ready", "stale", "synchronizing":
		return s.Utilization != nil && s.ObservedAt != nil && s.ResetsAt != nil
	case "unavailable", "error":
		return true
	default:
		return false
	}
}

func (s *ControlStore) MemberSubscription(ctx context.Context, actor int64, carID string) (NativeMemberSubscription, error) {
	return controlMemberRead(ctx, s, actor, carID, func(snap controlMemberSnapshot, credential string) (NativeMemberSubscription, error) {
		return s.client.MemberSubscription(ctx, snap.connection.origin, credential, snap.target, snap.subject)
	})
}

func (s *ControlStore) NativeQuota(ctx context.Context, actor int64, carID string, force bool) (NativeQuotaStatus, error) {
	snap, err := s.nativeConnection(ctx, actor, carID)
	if err != nil {
		return NativeQuotaStatus{}, err
	}
	key, err := s.memberCredential(snap)
	if err != nil {
		return NativeQuotaStatus{}, err
	}
	result, err := s.client.NativeQuota(ctx, snap.connection.origin, key, snap.target, force)
	if err != nil {
		return NativeQuotaStatus{}, err
	}
	current, err := s.nativeConnection(ctx, actor, carID)
	if err != nil {
		return NativeQuotaStatus{}, err
	}
	if current.target != snap.target || current.connection.origin != snap.connection.origin || current.connection.generation != snap.connection.generation {
		return NativeQuotaStatus{}, ErrMainConflict
	}
	return result, nil
}
