package carpool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"
)

type ControlPageQuery struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

func (q ControlPageQuery) Validate() error {
	if q.Page < 1 || q.Page > 10000 || q.PageSize < 1 || q.PageSize > 100 {
		return ErrMainInvalid
	}
	return nil
}

type ControlUsageQuery struct {
	ControlPageQuery
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

func (q ControlUsageQuery) Range() (time.Time, time.Time, error) {
	start, err := time.Parse(time.DateOnly, q.StartDate)
	if err != nil {
		return time.Time{}, time.Time{}, ErrMainInvalid
	}
	end, err := time.Parse(time.DateOnly, q.EndDate)
	if err != nil || end.Before(start) {
		return time.Time{}, time.Time{}, ErrMainInvalid
	}
	end = end.AddDate(0, 0, 1)
	if end.Sub(start) > 31*24*time.Hour {
		return time.Time{}, time.Time{}, ErrMainInvalid
	}
	return start, end, nil
}

type ControlPage[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Pages    int   `json:"pages"`
}

func validControlPage[T any](page ControlPage[T], q ControlPageQuery) bool {
	return page.Items != nil && len(page.Items) <= q.PageSize && page.Total >= int64(len(page.Items)) && page.Page == q.Page && page.PageSize == q.PageSize && page.Pages >= 0
}

type ManagedUsage struct {
	ID                  int64     `json:"id"`
	APIKeyID            int64     `json:"api_key_id"`
	Model               string    `json:"model"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	CacheCreationTokens int64     `json:"cache_creation_tokens"`
	CacheReadTokens     int64     `json:"cache_read_tokens"`
	TotalCost           float64   `json:"total_cost"`
	ActualCost          float64   `json:"actual_cost"`
	CreatedAt           time.Time `json:"created_at"`
}

type ManagedUsageStats struct {
	TotalRequests            int64   `json:"total_requests"`
	TotalTokens              int64   `json:"total_tokens"`
	TotalInputTokens         int64   `json:"total_input_tokens"`
	TotalOutputTokens        int64   `json:"total_output_tokens"`
	TotalCacheCreationTokens int64   `json:"total_cache_creation_tokens"`
	TotalCacheReadTokens     int64   `json:"total_cache_read_tokens"`
	TotalCost                float64 `json:"total_cost"`
	TotalActualCost          float64 `json:"total_actual_cost"`
}

func controlPageValues(q ControlPageQuery) url.Values {
	return url.Values{"page": {strconv.Itoa(q.Page)}, "page_size": {strconv.Itoa(q.PageSize)}, "sort_by": {"created_at"}, "sort_order": {"desc"}}
}

func controlUsageValues(q ControlUsageQuery, userID, groupID int64) url.Values {
	values := controlPageValues(q.ControlPageQuery)
	values.Set("user_id", strconv.FormatInt(userID, 10))
	values.Set("group_id", strconv.FormatInt(groupID, 10))
	values.Set("start_date", q.StartDate)
	values.Set("end_date", q.EndDate)
	values.Set("timezone", "UTC")
	values.Set("exact_total", "true")
	values.Set("nocache", "true")
	return values
}

func (c *ControlClient) verifyReadTarget(ctx context.Context, origin, credential string, target ManagedTarget) error {
	if err := target.Validate(); err != nil {
		return err
	}
	info, err := c.Info(ctx, origin, credential)
	if err != nil {
		return err
	}
	if info.MainID != target.MainID || info.NodeID != target.NodeID || info.CarID != target.CarID || info.InstanceID != target.InstanceID {
		return ErrControlTarget
	}
	if !slices.Contains(info.Capabilities, "bound-admin-reads") {
		return ErrControlProtocol
	}
	return nil
}

func (c *ControlClient) ListMemberKeys(ctx context.Context, origin, credential string, target ManagedTarget, userID, groupID int64, q ControlPageQuery) (ControlPage[ManagedKey], error) {
	var result ControlPage[ManagedKey]
	if userID <= 0 || groupID <= 0 || q.Validate() != nil {
		return result, ErrMainInvalid
	}
	if err := c.verifyReadTarget(ctx, origin, credential, target); err != nil {
		return result, err
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodGet, "/api/v1/admin/users/"+strconv.FormatInt(userID, 10)+"/api-keys?"+controlPageValues(q).Encode(), nil, &target)
	if err != nil {
		return result, err
	}
	var page ControlPage[struct {
		ManagedKey
		UserID  int64  `json:"user_id"`
		GroupID *int64 `json:"group_id"`
	}]
	if json.Unmarshal(data, &page) != nil || !validControlPage(page, q) {
		return result, ErrControlProtocol
	}
	result = ControlPage[ManagedKey]{Items: make([]ManagedKey, 0, len(page.Items)), Total: page.Total, Page: page.Page, PageSize: page.PageSize, Pages: page.Pages}
	for _, key := range page.Items {
		if key.UserID != userID || key.GroupID == nil || *key.GroupID != groupID || key.ID <= 0 {
			return ControlPage[ManagedKey]{}, ErrControlProtocol
		}
		key.Key = ""
		result.Items = append(result.Items, key.ManagedKey)
	}
	return result, nil
}

func (c *ControlClient) MemberUsage(ctx context.Context, origin, credential string, target ManagedTarget, userID, groupID int64, q ControlUsageQuery) (ControlPage[ManagedUsage], error) {
	var result ControlPage[ManagedUsage]
	start, end, err := q.Range()
	if err != nil || userID <= 0 || groupID <= 0 || q.Validate() != nil {
		return result, ErrMainInvalid
	}
	if err := c.verifyReadTarget(ctx, origin, credential, target); err != nil {
		return result, err
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodGet, "/api/v1/admin/usage?"+controlUsageValues(q, userID, groupID).Encode(), nil, &target)
	if err != nil {
		return result, err
	}
	var page ControlPage[struct {
		ManagedUsage
		UserID  int64  `json:"user_id"`
		GroupID *int64 `json:"group_id"`
	}]
	if json.Unmarshal(data, &page) != nil || !validControlPage(page, q.ControlPageQuery) {
		return result, ErrControlProtocol
	}
	result = ControlPage[ManagedUsage]{Items: make([]ManagedUsage, 0, len(page.Items)), Total: page.Total, Page: page.Page, PageSize: page.PageSize, Pages: page.Pages}
	for _, record := range page.Items {
		if record.UserID != userID || record.GroupID == nil || *record.GroupID != groupID || record.ID <= 0 || record.CreatedAt.Before(start) || !record.CreatedAt.Before(end) || record.InputTokens < 0 || record.OutputTokens < 0 || record.CacheCreationTokens < 0 || record.CacheReadTokens < 0 || record.TotalCost < 0 || record.ActualCost < 0 {
			return ControlPage[ManagedUsage]{}, ErrControlProtocol
		}
		result.Items = append(result.Items, record.ManagedUsage)
	}
	return result, nil
}

func (c *ControlClient) MemberUsageStats(ctx context.Context, origin, credential string, target ManagedTarget, userID, groupID int64, q ControlUsageQuery) (ManagedUsageStats, error) {
	var result ManagedUsageStats
	_, _, err := q.Range()
	if err != nil || userID <= 0 || groupID <= 0 || q.Validate() != nil {
		return result, ErrMainInvalid
	}
	if err := c.verifyReadTarget(ctx, origin, credential, target); err != nil {
		return result, err
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodGet, "/api/v1/admin/usage/stats?"+controlUsageValues(q, userID, groupID).Encode(), nil, &target)
	if err != nil {
		return result, err
	}
	var required struct {
		Requests *int64   `json:"total_requests"`
		Tokens   *int64   `json:"total_tokens"`
		Cost     *float64 `json:"total_actual_cost"`
	}
	if json.Unmarshal(data, &required) != nil || required.Requests == nil || required.Tokens == nil || required.Cost == nil || json.Unmarshal(data, &result) != nil || result.TotalRequests < 0 || result.TotalTokens < 0 || result.TotalInputTokens < 0 || result.TotalOutputTokens < 0 || result.TotalCacheCreationTokens < 0 || result.TotalCacheReadTokens < 0 || result.TotalCost < 0 || result.TotalActualCost < 0 {
		return ManagedUsageStats{}, ErrControlProtocol
	}
	return result, nil
}
