package controlplane

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
)

func TestWhiteListSidecarBeforeSendReturnsActionToPendingWithoutReceiptLookup(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	desired := seedWhiteListSidecarActionFixture(t, db, "origin-s4", "s4", testDigest("a"))
	sender := &beforeSendThenReceiptSender{receipt: desiredReceiptSender{
		now: service.clock.Now(), bootID: "boot-s4",
	}}

	if _, err := service.ExecuteWhiteListSidecarAction(
		context.Background(), desired, "panel-a", sender,
	); !errors.Is(err, sidecaragentclient.ErrBeforeSend) {
		t.Fatalf("first ExecuteWhiteListSidecarAction error=%v", err)
	}
	if sender.lookups != 0 || sender.posts != 1 {
		t.Fatalf("before-send posts=%d lookups=%d, want 1/0", sender.posts, sender.lookups)
	}
	assertExternalActionState(t, db, desired.Action.ActionKey, "pending", 1)

	receipt, err := service.ExecuteWhiteListSidecarAction(
		context.Background(), desired, "panel-a", sender,
	)
	if err != nil {
		t.Fatalf("retry ExecuteWhiteListSidecarAction: %v", err)
	}
	if receipt.ActionKey != desired.Action.ActionKey || sender.posts != 2 || sender.lookups != 0 {
		t.Fatalf("receipt=%#v posts=%d lookups=%d", receipt, sender.posts, sender.lookups)
	}
	assertExternalActionState(t, db, desired.Action.ActionKey, "applied", 2)
}

type beforeSendThenReceiptSender struct {
	posts   int
	lookups int
	receipt desiredReceiptSender
}

func (sender *beforeSendThenReceiptSender) Post(ctx context.Context, request []byte) ([]byte, error) {
	sender.posts++
	if sender.posts == 1 {
		return nil, sidecaragentclient.ErrBeforeSend
	}
	return sender.receipt.Post(ctx, request)
}

func (sender *beforeSendThenReceiptSender) LookupReceipt(context.Context, string) ([]byte, error) {
	sender.lookups++
	return nil, sidecaragentclient.ErrReceiptNotFound
}

func assertExternalActionState(
	t *testing.T, db *customerIntegritySQLite, actionKey, state string, attempts int64,
) {
	t.Helper()
	rows := db.must(t, rqlite.Statement{SQL: `SELECT status,attempts FROM external_actions WHERE idempotency_key=?`, Args: []any{actionKey}})[0].Rows
	want := fmt.Sprintf("[map[attempts:%d status:%s]]", attempts, state)
	if fmt.Sprint(rows) != want {
		t.Fatalf("external action rows=%#v, want %s", rows, want)
	}
}

var _ ExternalActionSender = (*beforeSendThenReceiptSender)(nil)
var _ whiteListSidecarReceiptLookup = (*beforeSendThenReceiptSender)(nil)
