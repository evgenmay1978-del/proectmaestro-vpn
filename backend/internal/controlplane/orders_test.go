package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestActorChannelSourceEventAndTimeDoNotChangeHash(t *testing.T) {
	wantJSON := `{"decision":"confirm","order_id":"ord_hash","payment_reference":"receipt_hash","tariff_version":"tariff_1m_v1"}`
	wantDigest := sha256.Sum256([]byte(wantJSON))
	want := hex.EncodeToString(wantDigest[:])

	first := ConfirmPaymentCommand{
		OrderID: "ord_hash", IdempotencyKey: "confirm-hash", PaymentReference: "receipt_hash",
		TariffVersionID: "tariff_1m_v1", Actor: "owner-a", Channel: "telegram",
		SourceEventID: "callback-a", OccurredAt: time.Unix(100, 0),
	}
	second := first
	second.Actor = "owner-b"
	second.Channel = "web"
	second.SourceEventID = "callback-b"
	second.OccurredAt = time.Unix(200, 0)
	second.ProposedPaymentID = "payment_retry_value"
	second.ProposedOperationID = "operation_retry_value"

	gotFirst, err := canonicalPaymentDecisionHash(first, "confirm")
	if err != nil {
		t.Fatalf("first canonical hash: %v", err)
	}
	gotSecond, err := canonicalPaymentDecisionHash(second, "confirm")
	if err != nil {
		t.Fatalf("second canonical hash: %v", err)
	}
	if gotFirst != want || gotSecond != want {
		t.Fatalf("hashes=(%q,%q), want %q", gotFirst, gotSecond, want)
	}
}

func TestOrderDecisionConflictPreservesStatementEvidence(t *testing.T) {
	statementErr := &rqlite.StatementError{Index: 7, Message: "synthetic guard failure"}
	err := orderDecisionConflict(statementErr)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v, want ErrConflict", err)
	}
	var preserved *rqlite.StatementError
	if !errors.As(err, &preserved) || preserved != statementErr {
		t.Fatalf("statement error not preserved: %v", err)
	}
	if !strings.Contains(err.Error(), "statement 7 failed: synthetic guard failure") {
		t.Fatalf("statement evidence missing: %v", err)
	}
}
