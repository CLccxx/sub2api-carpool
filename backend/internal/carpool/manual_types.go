package carpool

import "time"

type ManualEnrollmentInput struct {
	UserID          int64     `json:"user_id"`
	AmountMinor     int64     `json:"amount_minor"`
	Channel         string    `json:"channel"`
	ReceivedAt      time.Time `json:"received_at"`
	Reference       string    `json:"reference"`
	Note            string    `json:"note"`
	ConfirmReceived bool      `json:"confirm_received"`
}

type ManualWithdrawalInput struct {
	Reason  string `json:"reason"`
	Confirm bool   `json:"confirm"`
}

type ManualRefundInput struct {
	AmountMinor     int64     `json:"amount_minor"`
	RefundedAt      time.Time `json:"refunded_at"`
	Reference       string    `json:"reference"`
	ConfirmRefunded bool      `json:"confirm_refunded"`
}

type ManualRefundSummary struct {
	Status      string     `json:"status"`
	AmountMinor int64      `json:"amount_minor"`
	RequestedAt *time.Time `json:"requested_at"`
	RefundedAt  *time.Time `json:"refunded_at"`
}

type ManualPayment struct {
	AmountMinor int64                `json:"amount_minor"`
	Currency    string               `json:"currency"`
	Channel     string               `json:"channel"`
	ReceivedAt  time.Time            `json:"received_at"`
	RecordedAt  time.Time            `json:"recorded_at"`
	Refund      *ManualRefundSummary `json:"refund"`
}

type ManualReceipt struct {
	RecordedBy int64  `json:"recorded_by"`
	Reference  string `json:"reference"`
	Note       string `json:"note"`
}

type ManualRefundRecord struct {
	Reason      string     `json:"reason"`
	RequestedBy *int64     `json:"requested_by"`
	RecordedAt  *time.Time `json:"recorded_at"`
	RecordedBy  *int64     `json:"recorded_by"`
	Reference   string     `json:"reference"`
}

type ManualOrder struct {
	Order
	UserEmail    string              `json:"user_email"`
	Receipt      ManualReceipt       `json:"receipt"`
	RefundRecord *ManualRefundRecord `json:"refund_record"`
}

type ManualOrderQuery struct {
	PageQuery
	CarID        string
	RefundStatus string
}

type ManualOrderPage struct {
	PageMeta
	Items []ManualOrder `json:"items"`
}
