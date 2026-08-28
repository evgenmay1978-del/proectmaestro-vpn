package controlplane

import (
	"context"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func (s *Service) LegacyOrderVisibility(ctx context.Context, orderID string) (string, error) {
	if orderID == "" {
		return "", ErrNotFound
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT o.payment_state,
CASE WHEN c.status='active' AND c.expires_at_unix > unixepoch() THEN 1 ELSE 0 END AS subscription_valid,
CASE WHEN EXISTS (
  SELECT 1 FROM node_services ns
  JOIN nodes n ON n.node_id=ns.node_id
  JOIN node_apply_receipts r ON r.customer_id=o.customer_id
    AND r.node_id=ns.node_id AND r.service_name=ns.service_name
  WHERE ns.desired_target=1 AND ns.apply_enabled=1 AND ns.fenced=0 AND ns.retired=0
    AND n.enabled=1 AND r.status='applied' AND r.generation >= o.result_generation
) THEN 1 ELSE 0 END AS receipt_ready
FROM orders o JOIN customers c ON c.customer_id=o.customer_id
WHERE o.order_id=?`,
		Args: []any{orderID},
	})
	if err != nil {
		return "", ErrUnavailable
	}
	if len(results) != 1 || len(results[0].Rows) != 1 {
		return "", ErrNotFound
	}
	row := results[0].Rows[0]
	payment, paymentOK := rowString(row, "payment_state")
	valid, validOK := rowInt64(row, "subscription_valid")
	ready, readyOK := rowInt64(row, "receipt_ready")
	if !paymentOK || !validOK || !readyOK {
		return "", ErrUnavailable
	}
	if payment == "confirmed" && valid == 1 && ready == 1 {
		return "paid", nil
	}
	return "pending", nil
}
