package carpool

import (
	"context"
	"encoding/json"
	"time"
)

// Only these explicit fields are exposed to members, never a raw receipt row.
const manualPaymentJSON = `(SELECT jsonb_build_object('amount_minor',r.amount_minor,'currency',r.currency,'channel',r.channel,
 'received_at',r.received_at,'recorded_at',r.recorded_at,'refund',
 CASE WHEN f.order_id IS NOT NULL OR (` + effectiveOrderStatus + `)='cancelled' THEN
 jsonb_build_object('status',COALESCE(f.status,'pending'),'amount_minor',r.amount_minor,'requested_at',f.requested_at,'refunded_at',f.refunded_at) ELSE NULL END)
 FROM carpool_manual_receipts r LEFT JOIN carpool_manual_refunds f ON f.order_id=r.order_id WHERE r.order_id=o.id)`

func readManualOrder(ctx context.Context, q mainQueryer, id string) (ManualOrder, error) {
	var result ManualOrder
	order, err := readMainOrder(ctx, q, id)
	if err != nil {
		return result, err
	}
	if order.Mode != "manual" {
		return result, ErrMainNotFound
	}
	result.Order = order
	var refund []byte
	err = q.QueryRowContext(ctx, `SELECT COALESCE(to_jsonb(u)->>'email',''),r.recorded_by,COALESCE(r.reference,''),r.note,
	 CASE WHEN f.order_id IS NULL THEN NULL ELSE to_jsonb(f)||jsonb_build_object('reference',COALESCE(f.reference,'')) END
	 FROM carpool_manual_receipts r JOIN carpool_orders o ON o.id=r.order_id JOIN users u ON u.id=o.user_id
	 LEFT JOIN carpool_manual_refunds f ON f.order_id=r.order_id WHERE r.order_id=$1`, id).Scan(&result.UserEmail, &result.Receipt.RecordedBy, &result.Receipt.Reference, &result.Receipt.Note, &refund)
	if err != nil {
		return result, err
	}
	if len(refund) > 0 {
		err = json.Unmarshal(refund, &result.RefundRecord)
	}
	return result, err
}

func (s *MainService) GetManualOrder(ctx context.Context, orderID string) (ManualOrder, error) {
	if err := s.ready(); err != nil {
		return ManualOrder{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := readManualOrder(ctx, s.db, orderID)
	return result, mainDBError(err)
}

func (s *MainService) ListManualOrders(ctx context.Context, query ManualOrderQuery) (ManualOrderPage, error) {
	page := ManualOrderPage{Items: []ManualOrder{}}
	if err := s.ready(); err != nil {
		return page, err
	}
	q, err := query.normalized("confirmed", "cancelled")
	if err != nil {
		return page, err
	}
	if query.RefundStatus != "" && query.RefundStatus != "none" && query.RefundStatus != "pending" && query.RefundStatus != "refunded" {
		return page, ErrMainInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	page.Page, page.PageSize = q.Page, q.PageSize
	from := ` FROM carpool_orders o JOIN carpool_cars c ON c.id=o.car_id JOIN carpool_manual_receipts r ON r.order_id=o.id LEFT JOIN carpool_manual_refunds f ON f.order_id=o.id
	 WHERE o.mode='manual' AND ($1='' OR o.car_id=$1) AND ($2='' OR (` + effectiveOrderStatus + `)=$2)
	 AND ($3='' OR COALESCE(f.status,CASE WHEN (` + effectiveOrderStatus + `)='cancelled' THEN 'pending' ELSE 'none' END)=$3)`
	err = s.db.QueryRowContext(ctx, `SELECT count(*),statement_timestamp()`+from, query.CarID, q.Status, query.RefundStatus).Scan(&page.Total, &page.ServerTime)
	if err != nil {
		return page, mainDBError(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.id`+from+` ORDER BY o.created_at DESC,o.id LIMIT $4 OFFSET $5`, query.CarID, q.Status, query.RefundStatus, q.PageSize, (q.Page-1)*q.PageSize)
	if err != nil {
		return page, mainDBError(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return page, mainDBError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return page, mainDBError(err)
	}
	for _, id := range ids {
		order, err := readManualOrder(ctx, s.db, id)
		if err != nil {
			return page, mainDBError(err)
		}
		page.Items = append(page.Items, order)
	}
	return page, nil
}
