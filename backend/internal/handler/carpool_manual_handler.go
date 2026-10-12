package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func carpoolManualKey(c *gin.Context) (string, bool) {
	key := c.GetHeader("Idempotency-Key")
	if key == "" || len(key) > 128 || key != strings.TrimSpace(key) {
		response.BadRequest(c, "Idempotency-Key is required and must be 1-128 non-space ASCII characters")
		return "", false
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			response.BadRequest(c, "Invalid Idempotency-Key")
			return "", false
		}
	}
	return key, true
}

func (h *CarpoolHandler) EnrollManual(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	key, ok := carpoolManualKey(c)
	if !ok {
		return
	}
	var input carpool.ManualEnrollmentInput
	if !carpoolJSON(c, &input, false) {
		return
	}
	result, err := h.main.EnrollManual(c.Request.Context(), actor, c.Param("id"), key, input)
	carpoolResult(c, result, err)
}

func (h *CarpoolHandler) WithdrawManual(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	key, ok := carpoolManualKey(c)
	if !ok {
		return
	}
	var input carpool.ManualWithdrawalInput
	if !carpoolJSON(c, &input, false) {
		return
	}
	result, err := h.main.WithdrawManual(c.Request.Context(), actor, c.Param("id"), key, input)
	carpoolResult(c, result, err)
}

func (h *CarpoolHandler) RefundManual(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	key, ok := carpoolManualKey(c)
	if !ok {
		return
	}
	var input carpool.ManualRefundInput
	if !carpoolJSON(c, &input, false) {
		return
	}
	result, err := h.main.RefundManual(c.Request.Context(), actor, c.Param("id"), key, input)
	carpoolResult(c, result, err)
}

func (h *CarpoolHandler) AdminGetManualOrder(c *gin.Context) {
	if _, ok := h.actor(c); !ok {
		return
	}
	result, err := h.main.GetManualOrder(c.Request.Context(), c.Param("id"))
	carpoolResult(c, result, err)
}

func (h *CarpoolHandler) AdminListManualOrders(c *gin.Context) {
	if _, ok := h.actor(c); !ok {
		return
	}
	query := carpool.ManualOrderQuery{PageQuery: carpoolPage(c), CarID: c.Query("car_id"), RefundStatus: c.Query("refund_status")}
	result, err := h.main.ListManualOrders(c.Request.Context(), query)
	carpoolResult(c, result, err)
}
