package controlplane

import (
	"context"
	"strings"
	"testing"
)

func TestPaidVisibilityRequiresConfirmedUsableSubscriptionAndReceipt(t *testing.T) {
	tests := []struct {
		name string
		row  map[string]any
		want string
	}{
		{"unconfirmed", map[string]any{"payment_state": "claimed", "subscription_valid": 1, "receipt_ready": 1}, "pending"},
		{"expired", map[string]any{"payment_state": "confirmed", "subscription_valid": 0, "receipt_ready": 1}, "pending"},
		{"no-receipt", map[string]any{"payment_state": "confirmed", "subscription_valid": 1, "receipt_ready": 0}, "pending"},
		{"usable", map[string]any{"payment_state": "confirmed", "subscription_valid": 1, "receipt_ready": 1}, "paid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{linear: []scriptedResult{rowsScript(test.row)}}
			service, _ := testService(t, db)
			got, err := service.LegacyOrderVisibility(context.Background(), "order-1")
			if err != nil {
				t.Fatalf("LegacyOrderVisibility: %v", err)
			}
			if got != test.want {
				t.Fatalf("visibility = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPaidVisibilityAcceptsReceiptNewerThanResultGeneration(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(map[string]any{
		"payment_state": "confirmed", "subscription_valid": 1, "receipt_ready": 1,
	})}}
	service, _ := testService(t, db)
	got, err := service.LegacyOrderVisibility(context.Background(), "order-1")
	if err != nil || got != "paid" {
		t.Fatalf("visibility = %q, err=%v", got, err)
	}
	if len(db.linearCalls) != 1 {
		t.Fatalf("linear calls = %d", len(db.linearCalls))
	}
	sql := strings.ToLower(db.linearCalls[0].statements[0].SQL)
	if !strings.Contains(sql, "generation >= o.result_generation") {
		t.Fatalf("paid gate does not accept newer receipt generation: %s", sql)
	}
}
