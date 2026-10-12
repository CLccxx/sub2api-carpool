package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/gin-gonic/gin"
)

func (h *CarpoolControlHandler) InitializeNativeSubscription(c *gin.Context) {
	if _, ok := controlActor(c, h.NodeEnabled()); !ok || !noMemberQuery(c) {
		return
	}
	var input carpool.NativeSetupInput
	if !carpoolJSON(c, &input, false) {
		return
	}
	result, err := h.node.InitializeSubscription(c.Request.Context(), managedRequestTarget(c), input)
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) NativeAccounts(c *gin.Context) {
	if _, ok := controlActor(c, h.NodeEnabled()); !ok || !noMemberQuery(c) {
		return
	}
	result, err := h.node.SubscriptionAccounts(c.Request.Context(), managedRequestTarget(c))
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) CarNativeStatus(c *gin.Context) {
	if _, ok := controlActor(c, h.MainEnabled() && h.nativeMain != nil); !ok || !noMemberQuery(c) {
		return
	}
	result, err := h.nativeMain.NativeCarStatus(c.Request.Context(), c.Param("id"))
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) CarNativeAccounts(c *gin.Context) {
	actor, ok := controlActor(c, h.MainEnabled() && h.nativeMain != nil)
	if !ok || !noMemberQuery(c) {
		return
	}
	result, err := h.nativeMain.NativeAccounts(c.Request.Context(), actor, c.Param("id"))
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) PrepareNativeCar(c *gin.Context) {
	actor, ok := controlActor(c, h.MainEnabled() && h.nativeMain != nil)
	if !ok || !noMemberQuery(c) {
		return
	}
	var input struct {
		AccountID int64 `json:"account_id"`
	}
	if !carpoolJSON(c, &input, false) {
		return
	}
	result, err := h.nativeMain.PrepareNativeCar(c.Request.Context(), actor, c.Param("id"), input.AccountID)
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) ActivateNativeCar(c *gin.Context) {
	actor, ok := controlActor(c, h.MainEnabled() && h.nativeMain != nil)
	if !ok || !noMemberQuery(c) {
		return
	}
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if !carpoolJSON(c, &input, false) {
		return
	}
	result, err := h.nativeMain.ActivateNativeCar(c.Request.Context(), actor, c.Param("id"), input.Confirm)
	carpoolResult(c, result, err)
}
