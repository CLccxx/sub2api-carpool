package handler

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type carpoolManagedKeysAPI interface {
	Create(context.Context, carpool.ManagedTarget, string, string, string) (service.ManagedKey, error)
	Update(context.Context, carpool.ManagedTarget, string, int64, service.ManagedKeyUpdate) (service.ManagedKey, error)
	Delete(context.Context, carpool.ManagedTarget, string, int64) error
}

func (h *CarpoolControlHandler) CreateMemberKey(c *gin.Context) {
	if _, ok := controlActor(c, h.NodeEnabled() && h.keys != nil); !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !carpoolJSON(c, &input, false) {
		return
	}
	key, err := h.keys.Create(c.Request.Context(), managedRequestTarget(c), c.Param("subject"), c.GetHeader("Idempotency-Key"), input.Name)
	carpoolResult(c, key, err)
}

func managedKeyID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("key_id"), 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != c.Param("key_id") {
		response.ErrorFrom(c, carpool.ErrMainInvalid)
		return 0, false
	}
	return id, true
}

func (h *CarpoolControlHandler) UpdateMemberKey(c *gin.Context) {
	if _, ok := controlActor(c, h.NodeEnabled() && h.keys != nil); !ok {
		return
	}
	id, ok := managedKeyID(c)
	if !ok {
		return
	}
	var input service.ManagedKeyUpdate
	if !carpoolJSON(c, &input, false) {
		return
	}
	key, err := h.keys.Update(c.Request.Context(), managedRequestTarget(c), c.Param("subject"), id, input)
	carpoolResult(c, key, err)
}

func (h *CarpoolControlHandler) DeleteMemberKey(c *gin.Context) {
	if _, ok := controlActor(c, h.NodeEnabled() && h.keys != nil); !ok {
		return
	}
	id, ok := managedKeyID(c)
	if !ok || !carpoolJSON(c, &struct{}{}, true) {
		return
	}
	if response.ErrorFrom(c, h.keys.Delete(c.Request.Context(), managedRequestTarget(c), c.Param("subject"), id)) {
		return
	}
	response.Success(c, gin.H{"id": id, "deleted": true})
}
