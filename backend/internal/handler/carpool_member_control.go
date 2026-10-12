package handler

import (
	"context"
	"net/url"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type carpoolMemberAPI interface {
	CreateMemberKey(context.Context, int64, string, string, string) (carpool.ManagedKey, error)
	UpdateMemberKey(context.Context, int64, string, int64, carpool.ManagedKeyUpdate) (carpool.ManagedKey, error)
	DeleteMemberKey(context.Context, int64, string, int64) error
	ListMemberKeys(context.Context, int64, string, carpool.ControlPageQuery) (carpool.ControlPage[carpool.ManagedKey], error)
	MemberUsage(context.Context, int64, string, carpool.ControlUsageQuery) (carpool.ControlPage[carpool.ManagedUsage], error)
	MemberUsageStats(context.Context, int64, string, carpool.ControlUsageQuery) (carpool.ManagedUsageStats, error)
}

func (h *CarpoolControlHandler) memberActor(c *gin.Context) (int64, bool) {
	return controlActor(c, h.MainEnabled() && h.members != nil)
}

func noMemberQuery(c *gin.Context) bool {
	if c.Request.URL.RawQuery != "" {
		response.ErrorFrom(c, carpool.ErrMainInvalid)
		return false
	}
	return true
}

func memberReadQuery(c *gin.Context, usage bool) (carpool.ControlUsageQuery, bool) {
	q := carpool.ControlUsageQuery{ControlPageQuery: carpool.ControlPageQuery{Page: 1, PageSize: 20}}
	invalid := func() (carpool.ControlUsageQuery, bool) {
		response.ErrorFrom(c, carpool.ErrMainInvalid)
		return q, false
	}
	if len(c.Request.URL.RawQuery) > 2048 {
		return invalid()
	}
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return invalid()
	}
	for name, entries := range values {
		if len(entries) != 1 {
			return invalid()
		}
		value := entries[0]
		switch name {
		case "page":
			q.Page, err = strconv.Atoi(value)
		case "page_size":
			q.PageSize, err = strconv.Atoi(value)
		case "start_date":
			if !usage {
				return invalid()
			}
			q.StartDate = value
		case "end_date":
			if !usage {
				return invalid()
			}
			q.EndDate = value
		default:
			return invalid()
		}
		if err != nil {
			return invalid()
		}
	}
	if q.Validate() != nil {
		return invalid()
	}
	if usage {
		if _, _, err = q.Range(); err != nil {
			return invalid()
		}
	}
	return q, true
}

func (h *CarpoolControlHandler) CreateCarKey(c *gin.Context) {
	actor, ok := h.memberActor(c)
	if !ok || !noMemberQuery(c) {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !carpoolJSON(c, &input, false) {
		return
	}
	key, err := h.members.CreateMemberKey(c.Request.Context(), actor, c.Param("id"), c.GetHeader("Idempotency-Key"), input.Name)
	carpoolResult(c, key, err)
}
func (h *CarpoolControlHandler) UpdateCarKey(c *gin.Context) {
	actor, ok := h.memberActor(c)
	if !ok || !noMemberQuery(c) {
		return
	}
	id, ok := managedKeyID(c)
	if !ok {
		return
	}
	var input carpool.ManagedKeyUpdate
	if !carpoolJSON(c, &input, false) {
		return
	}
	key, err := h.members.UpdateMemberKey(c.Request.Context(), actor, c.Param("id"), id, input)
	key.Key = ""
	carpoolResult(c, key, err)
}
func (h *CarpoolControlHandler) DeleteCarKey(c *gin.Context) {
	actor, ok := h.memberActor(c)
	if !ok || !noMemberQuery(c) {
		return
	}
	id, ok := managedKeyID(c)
	if !ok || !carpoolJSON(c, &struct{}{}, true) {
		return
	}
	if response.ErrorFrom(c, h.members.DeleteMemberKey(c.Request.Context(), actor, c.Param("id"), id)) {
		return
	}
	response.Success(c, gin.H{"id": id, "deleted": true})
}
func (h *CarpoolControlHandler) MemberKeys(c *gin.Context) {
	actor, ok := h.memberActor(c)
	if !ok {
		return
	}
	q, ok := memberReadQuery(c, false)
	if !ok {
		return
	}
	result, err := h.members.ListMemberKeys(c.Request.Context(), actor, c.Param("id"), q.ControlPageQuery)
	for i := range result.Items {
		result.Items[i].Key = ""
	}
	carpoolResult(c, result, err)
}
func (h *CarpoolControlHandler) CarUsage(c *gin.Context) {
	actor, ok := h.memberActor(c)
	if !ok {
		return
	}
	q, ok := memberReadQuery(c, true)
	if !ok {
		return
	}
	result, err := h.members.MemberUsage(c.Request.Context(), actor, c.Param("id"), q)
	carpoolResult(c, result, err)
}
func (h *CarpoolControlHandler) CarUsageStats(c *gin.Context) {
	actor, ok := h.memberActor(c)
	if !ok {
		return
	}
	q, ok := memberReadQuery(c, true)
	if !ok {
		return
	}
	result, err := h.members.MemberUsageStats(c.Request.Context(), actor, c.Param("id"), q)
	carpoolResult(c, result, err)
}
