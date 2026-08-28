package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestRedeemTrialIsOneCanonicalTransaction(t *testing.T) {
	db := canonicalMutationDB(false)
	service, _ := testService(t, db)
	got, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{
		Login: "Alice", Anchor: "anchor-1", DRMIdentity: "drm-1", Days: 7, IdempotencyKey: "trial-1",
	})
	if err != nil {
		t.Fatalf("RedeemTrial: %v", err)
	}
	if got.ID != "customer_1" || got.Generation != 2 {
		t.Fatalf("customer = %#v", got)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("request calls = %#v, want one transaction", db.requestCalls)
	}
	sql := strings.ToLower(joinedRequestSQL(db))
	for _, table := range []string{"idempotency_requests", "trial_redemptions", "customers", "desired_node_state", "outbox_events"} {
		if !strings.Contains(sql, table) {
			t.Fatalf("trial transaction does not touch %s: %s", table, sql)
		}
	}
}

func TestRedeemTrialQuorumFailureHasNoLocalPendingLedger(t *testing.T) {
	db := canonicalMutationDB(false)
	db.requestFn = func(_ []rqlite.Statement) ([]rqlite.Result, error) { return nil, errors.New("quorum unavailable") }
	service, _ := testService(t, db)
	_, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{
		Login: "Alice", Anchor: "anchor-1", DRMIdentity: "drm-1", Days: 7, IdempotencyKey: "trial-1",
	})
	if err == nil {
		t.Fatal("RedeemTrial succeeded without quorum")
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("request calls = %#v, want only the failed canonical transaction", db.requestCalls)
	}
}
