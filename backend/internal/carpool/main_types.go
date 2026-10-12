package carpool

import (
	"fmt"
	"strings"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrMainInvalid     = apperrors.BadRequest("CARPOOL_INVALID_INPUT", "invalid carpool input")
	ErrMainNotFound    = apperrors.NotFound("CARPOOL_NOT_FOUND", "carpool resource not found")
	ErrMainConflict    = apperrors.Conflict("CARPOOL_CONFLICT", "carpool state or reservation conflict")
	ErrMainUnavailable = apperrors.ServiceUnavailable("CARPOOL_UNAVAILABLE", "carpool service unavailable")
)

type MainConfig struct {
	Enabled                 bool `mapstructure:"enabled"`
	TestOrdersEnabled       bool `mapstructure:"test_orders_enabled"`
	ManualEnrollmentEnabled bool `mapstructure:"manual_enrollment_enabled"`
}

func (c MainConfig) Validate(nodeEnabled bool) error {
	if c.Enabled && nodeEnabled {
		return fmt.Errorf("carpool main and node modes are mutually exclusive")
	}
	if c.TestOrdersEnabled && !c.Enabled {
		return fmt.Errorf("carpool test orders require main mode")
	}
	if c.ManualEnrollmentEnabled && (!c.Enabled || c.TestOrdersEnabled) {
		return fmt.Errorf("carpool manual enrollment requires main mode and excludes test orders")
	}
	return nil
}

type CarInput struct {
	Name            string    `json:"name"`
	Capacity        int       `json:"capacity"`
	PriceMinor      int64     `json:"price_minor"`
	TotalPriceMinor *int64    `json:"total_price_minor,omitempty"`
	Currency        string    `json:"currency"`
	ServiceDays     int       `json:"service_days"`
	RecruitUntil    time.Time `json:"recruit_until"`
	DeliveryHours   int       `json:"delivery_hours"`
	RefundPolicy    string    `json:"refund_policy"`
}

func (v CarInput) Validate(now time.Time) error {
	if err := v.normalizePrice(); err != nil {
		return err
	}
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 160 || v.Capacity < 2 || v.Capacity > 4 || v.PriceMinor <= 0 || v.PriceMinor > 1_000_000_000_000 || v.Currency != "CNY" || v.ServiceDays < 1 || v.ServiceDays > 3650 || v.DeliveryHours < 1 || v.DeliveryHours > 8760 || !v.RecruitUntil.After(now) || v.RecruitUntil.After(now.AddDate(10, 0, 0)) || strings.TrimSpace(v.RefundPolicy) == "" || len(v.RefundPolicy) > 4000 {
		return ErrMainInvalid
	}
	return nil
}

func (v *CarInput) normalizePrice() error {
	if v.TotalPriceMinor == nil {
		return nil
	}
	if v.Capacity < 2 || v.Capacity > 4 || *v.TotalPriceMinor < int64(v.Capacity) || *v.TotalPriceMinor > 1_000_000_000_000 {
		return ErrMainInvalid
	}
	// Integer minor units keep CNY rounding consistent with the stored quote.
	v.PriceMinor = (*v.TotalPriceMinor + int64(v.Capacity/2)) / int64(v.Capacity)
	return nil
}

type Car struct {
	CarInput
	EnrollmentMode string     `json:"enrollment_mode"`
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	PublishedAt    *time.Time `json:"published_at"`
	FullAt         *time.Time `json:"full_at"`
	DeliveryDue    *time.Time `json:"delivery_due"`
	ConfirmedCount int        `json:"confirmed_count"`
	HeldCount      int        `json:"held_count"`
	AvailableCount int        `json:"available_count"`
	Reservable     bool       `json:"reservable"`
	ServerTime     time.Time  `json:"server_time"`
}

type Membership struct {
	ID           string              `json:"id"`
	CarID        string              `json:"car_id"`
	UserID       int64               `json:"user_id"`
	SeatNo       int                 `json:"seat_no"`
	OrderID      string              `json:"order_id"`
	Status       string              `json:"status"`
	CreatedAt    time.Time           `json:"created_at"`
	ServiceStart *time.Time          `json:"service_start"`
	ServiceEnd   *time.Time          `json:"service_end"`
	UserEmail    string              `json:"user_email,omitempty"`
	Payment      *ManualPayment      `json:"payment,omitempty"`
	Receipt      *ManualReceipt      `json:"receipt,omitempty"`
	RefundRecord *ManualRefundRecord `json:"refund_record,omitempty"`
}

type Order struct {
	ID          string         `json:"id"`
	CarID       string         `json:"car_id"`
	UserID      int64          `json:"user_id"`
	SeatNo      int            `json:"seat_no"`
	Mode        string         `json:"mode"`
	Status      string         `json:"status"`
	Snapshot    CarInput       `json:"snapshot"`
	ExpiresAt   *time.Time     `json:"expires_at"`
	ConfirmedAt *time.Time     `json:"confirmed_at"`
	CreatedAt   time.Time      `json:"created_at"`
	Membership  *Membership    `json:"membership"`
	Payment     *ManualPayment `json:"payment,omitempty"`
}

type NodeInput struct {
	CarID    string `json:"car_id"`
	Hostname string `json:"hostname"`
	Label    string `json:"label"`
}

type Node struct {
	ID        string    `json:"id"`
	CarID     string    `json:"car_id"`
	Hostname  string    `json:"hostname"`
	Label     string    `json:"label"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type PageQuery struct {
	Page, PageSize int
	Status         string
}

func (q PageQuery) normalized(statuses ...string) (PageQuery, error) {
	if q.Page < 0 || q.Page > 100000 || q.PageSize < 0 || q.PageSize > 100 {
		return q, ErrMainInvalid
	}
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Status != "" {
		found := false
		for _, v := range statuses {
			if q.Status == v {
				found = true
			}
		}
		if !found {
			return q, ErrMainInvalid
		}
	}
	return q, nil
}

type PageMeta struct {
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	PageSize   int       `json:"page_size"`
	ServerTime time.Time `json:"server_time"`
}

type CarPage struct {
	PageMeta
	Items []Car `json:"items"`
}
type OrderPage struct {
	PageMeta
	Items []Order `json:"items"`
}
type MemberPage struct {
	PageMeta
	Items []Membership `json:"items"`
}
