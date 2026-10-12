package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

const carpoolBodyLimit = 64 << 10

type carpoolMainAPI interface {
	Enabled() bool
	TestOrdersEnabled() bool
	CreateCar(context.Context, int64, carpool.CarInput) (carpool.Car, error)
	UpdateCar(context.Context, int64, string, carpool.CarInput) (carpool.Car, error)
	PublishCar(context.Context, int64, string) (carpool.Car, error)
	CancelCar(context.Context, int64, string, string, bool) (carpool.Car, error)
	ListCars(context.Context, bool, carpool.PageQuery) (carpool.CarPage, error)
	GetCar(context.Context, string, bool) (carpool.Car, error)
	Reserve(context.Context, int64, string, string) (carpool.Order, error)
	GetOrder(context.Context, int64, string) (carpool.Order, error)
	CancelOrder(context.Context, int64, string) (carpool.Order, error)
	Mine(context.Context, int64, carpool.PageQuery) (carpool.OrderPage, error)
	ListMembers(context.Context, string, carpool.PageQuery) (carpool.MemberPage, error)
	RegisterNode(context.Context, int64, carpool.NodeInput) (carpool.Node, error)
	EnrollManual(context.Context, int64, string, string, carpool.ManualEnrollmentInput) (carpool.ManualOrder, error)
	WithdrawManual(context.Context, int64, string, string, carpool.ManualWithdrawalInput) (carpool.ManualOrder, error)
	RefundManual(context.Context, int64, string, string, carpool.ManualRefundInput) (carpool.ManualOrder, error)
	GetManualOrder(context.Context, string) (carpool.ManualOrder, error)
	ListManualOrders(context.Context, carpool.ManualOrderQuery) (carpool.ManualOrderPage, error)
}

type CarpoolHandler struct {
	main        carpoolMainAPI
	nodeEnabled bool
}

func NewCarpoolHandler(main *carpool.MainService, cfg *config.Config) *CarpoolHandler {
	h := &CarpoolHandler{nodeEnabled: cfg.CarpoolNode.Enabled}
	if main != nil {
		h.main = main
	}
	return h
}

func (h *CarpoolHandler) Enabled() bool {
	return h != nil && !h.nodeEnabled && h.main != nil && h.main.Enabled()
}

func (h *CarpoolHandler) actor(c *gin.Context) (int64, bool) {
	if !h.Enabled() {
		response.NotFound(c, "Carpool is disabled")
		return 0, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return 0, false
	}
	return subject.UserID, true
}

// Read the complete bounded body, including trailing whitespace, before decoding.
func carpoolJSON(c *gin.Context, dst any, allowEmpty bool) bool {
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, carpoolBodyLimit+1))
	if err != nil {
		response.BadRequest(c, "Invalid request body")
		return false
	}
	if len(data) > carpoolBodyLimit {
		response.Error(c, http.StatusRequestEntityTooLarge, "Request body too large")
		return false
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 && allowEmpty {
		return true
	}
	if len(data) == 0 || data[0] != '{' {
		response.BadRequest(c, "Expected one JSON object")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		response.BadRequest(c, "Invalid JSON object or unknown fields")
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		response.BadRequest(c, "Expected one JSON object")
		return false
	}
	return true
}

func carpoolPage(c *gin.Context) carpool.PageQuery {
	page, size := response.ParsePagination(c)
	return carpool.PageQuery{Page: page, PageSize: size, Status: c.Query("status")}
}

func carpoolResult(c *gin.Context, result any, err error) {
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

func (h *CarpoolHandler) ListCars(c *gin.Context)      { h.listCars(c, false) }
func (h *CarpoolHandler) AdminListCars(c *gin.Context) { h.listCars(c, true) }
func (h *CarpoolHandler) listCars(c *gin.Context, admin bool) {
	if _, ok := h.actor(c); !ok {
		return
	}
	v, err := h.main.ListCars(c.Request.Context(), admin, carpoolPage(c))
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) GetCar(c *gin.Context)      { h.getCar(c, false) }
func (h *CarpoolHandler) AdminGetCar(c *gin.Context) { h.getCar(c, true) }
func (h *CarpoolHandler) getCar(c *gin.Context, admin bool) {
	if _, ok := h.actor(c); !ok {
		return
	}
	v, err := h.main.GetCar(c.Request.Context(), c.Param("id"), admin)
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) CreateCar(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	var input carpool.CarInput
	if !carpoolJSON(c, &input, false) {
		return
	}
	v, err := h.main.CreateCar(c.Request.Context(), actor, input)
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) UpdateCar(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	var input carpool.CarInput
	if !carpoolJSON(c, &input, false) {
		return
	}
	v, err := h.main.UpdateCar(c.Request.Context(), actor, c.Param("id"), input)
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) PublishCar(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok || !carpoolJSON(c, &struct{}{}, true) {
		return
	}
	v, err := h.main.PublishCar(c.Request.Context(), actor, c.Param("id"))
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) CancelCar(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	var input struct {
		Reason         string `json:"reason"`
		ConfirmMembers bool   `json:"confirm_members"`
	}
	if !carpoolJSON(c, &input, false) {
		return
	}
	v, err := h.main.CancelCar(c.Request.Context(), actor, c.Param("id"), input.Reason, input.ConfirmMembers)
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) Reserve(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	if !h.main.TestOrdersEnabled() {
		response.ErrorFrom(c, carpool.ErrMainNotFound)
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		response.BadRequest(c, "Idempotency-Key is required")
		return
	}
	if !carpoolJSON(c, &struct{}{}, true) {
		return
	}
	v, err := h.main.Reserve(c.Request.Context(), actor, c.Param("id"), key)
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) GetOrder(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	v, err := h.main.GetOrder(c.Request.Context(), actor, c.Param("id"))
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) CancelOrder(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok || !carpoolJSON(c, &struct{}{}, true) {
		return
	}
	v, err := h.main.CancelOrder(c.Request.Context(), actor, c.Param("id"))
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) Mine(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	v, err := h.main.Mine(c.Request.Context(), actor, carpoolPage(c))
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) ListMembers(c *gin.Context) {
	if _, ok := h.actor(c); !ok {
		return
	}
	v, err := h.main.ListMembers(c.Request.Context(), c.Param("id"), carpoolPage(c))
	carpoolResult(c, v, err)
}

func (h *CarpoolHandler) RegisterNode(c *gin.Context) {
	actor, ok := h.actor(c)
	if !ok {
		return
	}
	var input carpool.NodeInput
	if !carpoolJSON(c, &input, false) {
		return
	}
	v, err := h.main.RegisterNode(c.Request.Context(), actor, input)
	carpoolResult(c, v, err)
}
