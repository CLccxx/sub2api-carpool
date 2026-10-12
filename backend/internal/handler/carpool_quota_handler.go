package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type carpoolQuotaAPI interface {
	Reconcile(context.Context, bool) (carpool.NativeQuotaStatus, error)
}

func ProvideCarpoolControlHandler(main *carpool.ControlStore, node *carpool.NodeControl, keys *service.CarpoolManagedKeys, quota *service.CarpoolQuotaWorker) *CarpoolControlHandler {
	h := NewCarpoolControlHandler(main, node, keys)
	if quota != nil {
		h.quota = quota
	}
	return h
}

func (h *CarpoolControlHandler) NativeMemberSubscription(c *gin.Context) {
	if _, ok := controlActor(c, h.NodeEnabled()); !ok || !noMemberQuery(c) {
		return
	}
	result, err := h.node.MemberSubscription(c.Request.Context(), managedRequestTarget(c), c.Param("subject"))
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) NativeQuota(c *gin.Context) {
	if !h.quotaTarget(c) {
		return
	}
	result, err := h.node.NativeQuotaStatus(c.Request.Context())
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) quotaTarget(c *gin.Context) bool {
	if _, ok := controlActor(c, h.NodeEnabled()); !ok || !noMemberQuery(c) {
		return false
	}
	if managedRequestTarget(c).Validate() != nil || managedRequestTarget(c) != h.node.Target() {
		response.ErrorFrom(c, carpool.ErrControlTarget)
		return false
	}
	return true
}

func (h *CarpoolControlHandler) SyncNativeQuota(c *gin.Context) {
	if !h.quotaTarget(c) || !carpoolJSON(c, &struct{}{}, true) {
		return
	}
	if h.quota == nil {
		response.ErrorFrom(c, carpool.ErrUnavailable)
		return
	}
	result, err := h.quota.Reconcile(c.Request.Context(), true)
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) CarSubscription(c *gin.Context) {
	actor, ok := controlActor(c, h.MainEnabled() && h.nativeMain != nil)
	if !ok || !noMemberQuery(c) {
		return
	}
	result, err := h.nativeMain.MemberSubscription(c.Request.Context(), actor, c.Param("id"))
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) CarQuota(c *gin.Context)     { h.carQuota(c, false) }
func (h *CarpoolControlHandler) SyncCarQuota(c *gin.Context) { h.carQuota(c, true) }

func (h *CarpoolControlHandler) carQuota(c *gin.Context, force bool) {
	actor, ok := controlActor(c, h.MainEnabled() && h.nativeMain != nil)
	if !ok || !noMemberQuery(c) || (force && !carpoolJSON(c, &struct{}{}, true)) {
		return
	}
	result, err := h.nativeMain.NativeQuota(c.Request.Context(), actor, c.Param("id"), force)
	carpoolResult(c, result, err)
}
