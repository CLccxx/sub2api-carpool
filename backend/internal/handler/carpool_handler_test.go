package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type carpoolHandlerFake struct {
	called   bool
	actor    int64
	key      string
	err      error
	testsOff bool
	admin    bool
	id       string
	page     carpool.PageQuery
	input    carpool.CarInput
	node     carpool.NodeInput
	reason   string
	confirm  bool
}

func (f *carpoolHandlerFake) Enabled() bool           { return true }
func (f *carpoolHandlerFake) TestOrdersEnabled() bool { return !f.testsOff }
func (f *carpoolHandlerFake) Reserve(_ context.Context, actor int64, id, key string) (carpool.Order, error) {
	f.called, f.actor, f.key = true, actor, key
	return carpool.Order{ID: "order", CarID: id}, f.err
}

func (f *carpoolHandlerFake) ListCars(_ context.Context, admin bool, q carpool.PageQuery) (carpool.CarPage, error) {
	f.called, f.admin, f.page = true, admin, q
	return carpool.CarPage{PageMeta: carpool.PageMeta{Page: q.Page, PageSize: q.PageSize, ServerTime: time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)}, Items: []carpool.Car{}}, f.err
}
func (f *carpoolHandlerFake) GetCar(_ context.Context, id string, admin bool) (carpool.Car, error) {
	f.called, f.id, f.admin = true, id, admin
	return carpool.Car{ID: id}, f.err
}
func (f *carpoolHandlerFake) CreateCar(_ context.Context, actor int64, input carpool.CarInput) (carpool.Car, error) {
	f.called, f.actor, f.input = true, actor, input
	return carpool.Car{CarInput: input}, f.err
}
func (f *carpoolHandlerFake) UpdateCar(_ context.Context, actor int64, id string, input carpool.CarInput) (carpool.Car, error) {
	f.called, f.actor, f.id, f.input = true, actor, id, input
	return carpool.Car{ID: id, CarInput: input}, f.err
}
func (f *carpoolHandlerFake) PublishCar(_ context.Context, actor int64, id string) (carpool.Car, error) {
	f.called, f.actor, f.id = true, actor, id
	return carpool.Car{ID: id}, f.err
}
func (f *carpoolHandlerFake) CancelCar(_ context.Context, actor int64, id, reason string, confirm bool) (carpool.Car, error) {
	f.called, f.actor, f.id, f.reason, f.confirm = true, actor, id, reason, confirm
	return carpool.Car{ID: id}, f.err
}
func (f *carpoolHandlerFake) RegisterNode(_ context.Context, actor int64, input carpool.NodeInput) (carpool.Node, error) {
	f.called, f.actor, f.node = true, actor, input
	return carpool.Node{CarID: input.CarID}, f.err
}
func (f *carpoolHandlerFake) GetOrder(_ context.Context, actor int64, id string) (carpool.Order, error) {
	f.called, f.actor, f.id = true, actor, id
	return carpool.Order{ID: id}, f.err
}
func (f *carpoolHandlerFake) CancelOrder(ctx context.Context, actor int64, id string) (carpool.Order, error) {
	return f.GetOrder(ctx, actor, id)
}
func (f *carpoolHandlerFake) Mine(_ context.Context, actor int64, q carpool.PageQuery) (carpool.OrderPage, error) {
	f.called, f.actor, f.page = true, actor, q
	return carpool.OrderPage{}, f.err
}
func (f *carpoolHandlerFake) ListMembers(_ context.Context, id string, q carpool.PageQuery) (carpool.MemberPage, error) {
	f.called, f.id, f.page = true, id, q
	return carpool.MemberPage{}, f.err
}

func (f *carpoolHandlerFake) EnrollManual(_ context.Context, actor int64, id, key string, _ carpool.ManualEnrollmentInput) (carpool.ManualOrder, error) {
	f.called, f.actor, f.id, f.key = true, actor, id, key
	return carpool.ManualOrder{}, f.err
}
func (f *carpoolHandlerFake) WithdrawManual(_ context.Context, actor int64, id, key string, _ carpool.ManualWithdrawalInput) (carpool.ManualOrder, error) {
	f.called, f.actor, f.id, f.key = true, actor, id, key
	return carpool.ManualOrder{}, f.err
}
func (f *carpoolHandlerFake) RefundManual(_ context.Context, actor int64, id, key string, _ carpool.ManualRefundInput) (carpool.ManualOrder, error) {
	f.called, f.actor, f.id, f.key = true, actor, id, key
	return carpool.ManualOrder{}, f.err
}
func (f *carpoolHandlerFake) GetManualOrder(_ context.Context, id string) (carpool.ManualOrder, error) {
	f.called, f.id = true, id
	return carpool.ManualOrder{}, f.err
}
func (f *carpoolHandlerFake) ListManualOrders(_ context.Context, q carpool.ManualOrderQuery) (carpool.ManualOrderPage, error) {
	f.called, f.page = true, q.PageQuery
	return carpool.ManualOrderPage{}, f.err
}

func TestCarpoolCatalogOnlyAndAdminInputs(t *testing.T) {
	input := carpool.CarInput{Name: "car", Capacity: 4, PriceMinor: 9900, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), DeliveryHours: 24, RefundPolicy: "test policy"}
	body, err := json.Marshal(input)
	require.NoError(t, err)
	for _, tt := range []struct {
		name, body string
		method     func(*CarpoolHandler) gin.HandlerFunc
		check      func(*testing.T, *carpoolHandlerFake)
	}{
		{"list", "", func(h *CarpoolHandler) gin.HandlerFunc { return h.ListCars }, func(t *testing.T, f *carpoolHandlerFake) {
			require.False(t, f.admin)
			require.Equal(t, carpool.PageQuery{Page: 2, PageSize: 10, Status: "draft"}, f.page)
		}},
		{"admin list", "", func(h *CarpoolHandler) gin.HandlerFunc { return h.AdminListCars }, func(t *testing.T, f *carpoolHandlerFake) { require.True(t, f.admin) }},
		{"get", "", func(h *CarpoolHandler) gin.HandlerFunc { return h.GetCar }, func(t *testing.T, f *carpoolHandlerFake) { require.False(t, f.admin); require.Equal(t, "id", f.id) }},
		{"admin get", "", func(h *CarpoolHandler) gin.HandlerFunc { return h.AdminGetCar }, func(t *testing.T, f *carpoolHandlerFake) { require.True(t, f.admin) }},
		{"create", string(body), func(h *CarpoolHandler) gin.HandlerFunc { return h.CreateCar }, func(t *testing.T, f *carpoolHandlerFake) { require.Equal(t, input, f.input) }},
		{"update", string(body), func(h *CarpoolHandler) gin.HandlerFunc { return h.UpdateCar }, func(t *testing.T, f *carpoolHandlerFake) {
			require.Equal(t, input, f.input)
			require.Equal(t, "id", f.id)
		}},
		{"publish", "{}", func(h *CarpoolHandler) gin.HandlerFunc { return h.PublishCar }, func(t *testing.T, f *carpoolHandlerFake) { require.Equal(t, "id", f.id) }},
		{"cancel car", `{"reason":"operator request","confirm_members":true}`, func(h *CarpoolHandler) gin.HandlerFunc { return h.CancelCar }, func(t *testing.T, f *carpoolHandlerFake) {
			require.Equal(t, "operator request", f.reason)
			require.True(t, f.confirm)
		}},
		{"register node", `{"car_id":"c","hostname":"node.example","label":"node"}`, func(h *CarpoolHandler) gin.HandlerFunc { return h.RegisterNode }, func(t *testing.T, f *carpoolHandlerFake) {
			require.Equal(t, carpool.NodeInput{CarID: "c", Hostname: "node.example", Label: "node"}, f.node)
		}},
		{"order", "", func(h *CarpoolHandler) gin.HandlerFunc { return h.GetOrder }, func(t *testing.T, f *carpoolHandlerFake) { require.EqualValues(t, 42, f.actor) }},
		{"cancel order", "{}", func(h *CarpoolHandler) gin.HandlerFunc { return h.CancelOrder }, func(t *testing.T, f *carpoolHandlerFake) { require.EqualValues(t, 42, f.actor) }},
		{"mine", "", func(h *CarpoolHandler) gin.HandlerFunc { return h.Mine }, func(t *testing.T, f *carpoolHandlerFake) { require.EqualValues(t, 42, f.actor) }},
		{"members", "", func(h *CarpoolHandler) gin.HandlerFunc { return h.ListMembers }, func(t *testing.T, f *carpoolHandlerFake) { require.Equal(t, "id", f.id) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &carpoolHandlerFake{testsOff: true}
			h := &CarpoolHandler{main: f}
			r := gin.New()
			r.POST("/:id", func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
				tt.method(h)(c)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/id?page=2&page_size=10&status=draft", strings.NewReader(tt.body)))
			require.Equal(t, 200, w.Code, w.Body.String())
			require.True(t, f.called)
			tt.check(t, f)
			if tt.body != "" {
				require.EqualValues(t, 42, f.actor)
			}
			if tt.name == "list" {
				require.Contains(t, w.Body.String(), `"server_time":"2026-09-16T00:00:00Z"`)
			}
		})
	}
}

func TestCarpoolCatalogOnlyDeniesReservation(t *testing.T) {
	f := &carpoolHandlerFake{testsOff: true}
	h := &CarpoolHandler{main: f}
	r := gin.New()
	r.POST("/:id", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		h.Reserve(c)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/id", strings.NewReader("{}"))
	req.Header.Set("Idempotency-Key", "key")
	r.ServeHTTP(w, req)
	require.Equal(t, 404, w.Code)
	require.Contains(t, w.Body.String(), "CARPOOL_NOT_FOUND")
	require.False(t, f.called)
}

func TestCarpoolMutationsRejectUnknownFields(t *testing.T) {
	for _, method := range []func(*CarpoolHandler) gin.HandlerFunc{
		func(h *CarpoolHandler) gin.HandlerFunc { return h.CreateCar }, func(h *CarpoolHandler) gin.HandlerFunc { return h.UpdateCar },
		func(h *CarpoolHandler) gin.HandlerFunc { return h.PublishCar }, func(h *CarpoolHandler) gin.HandlerFunc { return h.CancelCar },
		func(h *CarpoolHandler) gin.HandlerFunc { return h.RegisterNode }, func(h *CarpoolHandler) gin.HandlerFunc { return h.CancelOrder },
	} {
		f := &carpoolHandlerFake{}
		h := &CarpoolHandler{main: f}
		r := gin.New()
		r.POST("/:id", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			method(h)(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/id", strings.NewReader(`{"actor_id":999}`)))
		require.Equal(t, 400, w.Code)
		require.False(t, f.called)
	}
}

func TestCarpoolReserveStrictInput(t *testing.T) {
	for _, tt := range []struct {
		name, body, key string
		auth            bool
		status          int
	}{
		{"empty body", "", "retry", true, 200},
		{"empty object", "{}", "retry", true, 200},
		{"missing key", "{}", "", true, 400},
		{"identity injection", `{"user_id":999}`, "retry", true, 400},
		{"unknown", `{"unexpected":true}`, "retry", true, 400},
		{"null", "null", "retry", true, 400},
		{"array", "[]", "retry", true, 400},
		{"multiple", "{} {}", "retry", true, 400},
		{"trailing garbage", "{} x", "retry", true, 400},
		{"oversized", "{}" + strings.Repeat(" ", 65537), "retry", true, 413},
		{"unauthenticated", "{}", "retry", false, 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &carpoolHandlerFake{}
			h := &CarpoolHandler{main: f}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tt.auth {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
				}
				c.Next()
			})
			r.POST("/cars/:id/reservations", h.Reserve)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/cars/car/reservations", strings.NewReader(tt.body))
			req.Header.Set("Idempotency-Key", tt.key)
			r.ServeHTTP(w, req)
			require.Equal(t, tt.status, w.Code, w.Body.String())
			require.Equal(t, tt.status == 200, f.called)
			if f.called {
				require.EqualValues(t, 42, f.actor)
				require.Equal(t, "retry", f.key)
			}
		})
	}
}

func TestCarpoolDomainErrorMapping(t *testing.T) {
	f := &carpoolHandlerFake{err: infraerrors.Conflict("FULL", "car is full")}
	h := &CarpoolHandler{main: f}
	r := gin.New()
	r.POST("/cars/:id/reservations", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		h.Reserve(c)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/cars/car/reservations", strings.NewReader("{}"))
	req.Header.Set("Idempotency-Key", "retry")
	r.ServeHTTP(w, req)
	require.Equal(t, 409, w.Code)
	require.Contains(t, w.Body.String(), "FULL")
}
