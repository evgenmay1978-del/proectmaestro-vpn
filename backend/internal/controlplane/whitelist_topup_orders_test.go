package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
)

func TestCreateWhiteListTopUpOrderPersistsTypedOrderAtomically(t *testing.T) {
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(),
			whiteListTopUpPreparedResult("active", 2_000_000+86400),
		},
		requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
			return make([]rqlite.Result, len(statements)), nil
		},
	}
	service, _ := testService(t, db)
	command := CreateWhiteListTopUpOrderCommand{
		EntitlementID: "wl-ent-customer-1", ProductID: "wl-gb-20-v1",
		IdempotencyKey: "create-request-1", BuyerScope: "telegram", BuyerIdentity: "chat-1",
		OriginBotID: "maestro", ChatIdentity: "chat-1", Actor: "customer-1", Channel: "telegram",
	}

	got, err := service.CreateWhiteListTopUpOrder(context.Background(), command)
	if err != nil {
		t.Fatalf("CreateWhiteListTopUpOrder: %v", err)
	}
	if got.OrderID == "" || got.PaymentCode == "" || got.EntitlementID != command.EntitlementID ||
		got.ProductID != command.ProductID || got.AmountMinor != 30000 || got.Currency != "RUB" ||
		got.Bytes != 20000000000 || got.PaymentState != PaymentPending {
		t.Fatalf("top-up order=%#v", got)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction || db.requestCalls[0].level != rqlite.Linearizable {
		t.Fatalf("top-up write calls=%#v", db.requestCalls)
	}
	joined := strings.ToLower(statementsText(db.requestCalls[0].statements))
	for _, required := range []string{
		"insert into idempotency_requests", "'whitelist_topup_create'",
		"insert into orders", "insert into whitelist_topup_orders",
		"update idempotency_requests set status='applied'",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("top-up create transaction missing %q: %s", required, joined)
		}
	}
	if strings.Contains(joined, "active_order_guards") {
		t.Fatal("GB order must not occupy the ordinary access-order guard")
	}
}

func TestCreateWhiteListTopUpOrderRejectsInactivePrimaryAccess(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{
		resultsScript(),
		whiteListTopUpPreparedResult("expired", 2_000_000-1),
	}}
	service, _ := testService(t, db)
	_, err := service.CreateWhiteListTopUpOrder(context.Background(), CreateWhiteListTopUpOrderCommand{
		EntitlementID: "wl-ent-customer-1", ProductID: "wl-gb-5-v1",
		IdempotencyKey: "create-request-expired", BuyerScope: "telegram", BuyerIdentity: "chat-1",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("inactive primary error=%v, want ErrConflict", err)
	}
	if len(db.requestCalls) != 0 {
		t.Fatalf("inactive primary performed %d write(s)", len(db.requestCalls))
	}
}

func TestClaimWhiteListTopUpPaymentRecordsClaimBeforeStateChange(t *testing.T) {
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(),
			whiteListTopUpOrderResultWithOrigin("payment-code-1", PaymentPending),
		},
		requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
			return make([]rqlite.Result, len(statements)), nil
		},
	}
	service, secrets := testService(t, db)
	got, err := service.ClaimWhiteListTopUpPayment(context.Background(), ClaimWhiteListTopUpPaymentCommand{
		OrderID: "whitelist-topup-order-1", IdempotencyKey: "claim-request-1",
		Actor: "customer-1", Channel: "telegram", SourceEventID: "update-1",
	})
	if err != nil {
		t.Fatalf("ClaimWhiteListTopUpPayment: %v", err)
	}
	if got.OrderID != "whitelist-topup-order-1" || got.PaymentState != PaymentClaimed {
		t.Fatalf("claimed order=%#v", got)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("claim calls=%#v", db.requestCalls)
	}
	joined := strings.ToLower(statementsText(db.requestCalls[0].statements))
	claimIndex := strings.Index(joined, "insert into whitelist_topup_payment_claims")
	stateIndex := strings.Index(joined, "update orders set payment_state='payment_claimed'")
	if claimIndex < 0 || stateIndex < 0 || claimIndex >= stateIndex {
		t.Fatalf("claim must be recorded before order state change: %s", joined)
	}
	var delivery *rqlite.Statement
	for index := range db.requestCalls[0].statements {
		statement := &db.requestCalls[0].statements[index]
		if strings.Contains(strings.ToLower(statement.SQL), "insert into telegram_delivery_outbox") {
			delivery = statement
			break
		}
	}
	if delivery == nil {
		t.Fatal("top-up claim did not queue an owner Telegram delivery")
	}
	envelopeBytes, ok := delivery.Args[3].([]byte)
	if !ok {
		t.Fatalf("Telegram envelope type=%T, want []byte", delivery.Args[3])
	}
	var envelope Envelope
	if err := json.Unmarshal(envelopeBytes, &envelope); err != nil {
		t.Fatalf("decode Telegram envelope: %v", err)
	}
	plaintext, err := secrets.Open(SecretScope{
		OwnerType: "telegram-delivery",
		OwnerID:   "owner-whitelist-topup-claim:whitelist-topup-order-1",
		Field:     "payload",
		Kind:      "owner-order-event",
	}, envelope)
	if err != nil {
		t.Fatalf("open Telegram envelope: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		t.Fatalf("decode Telegram payload: %v", err)
	}
	wantPayload := map[string]string{
		"event":                 "owner_whitelist_topup_payment_claim",
		"order_id":              "whitelist-topup-order-1",
		"confirm_callback_data": "mwcf:whitelist-topup-order-1",
		"reject_callback_data":  "mwrj:whitelist-topup-order-1",
	}
	if len(payload) != len(wantPayload) {
		t.Fatalf("Telegram payload=%#v, want exactly %#v", payload, wantPayload)
	}
	for key, want := range wantPayload {
		if payload[key] != want {
			t.Fatalf("Telegram payload[%q]=%q, want %q", key, payload[key], want)
		}
	}
	for _, key := range []string{"confirm_callback_data", "reject_callback_data"} {
		if len([]byte(payload[key])) > 64 {
			t.Fatalf("Telegram callback %q exceeds 64 bytes", payload[key])
		}
	}
}

func TestRejectWhiteListTopUpOrderIsTerminalWithoutCredit(t *testing.T) {
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(),
			whiteListTopUpOrderResult("payment-code-2", PaymentClaimed),
		},
		requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
			return make([]rqlite.Result, len(statements)), nil
		},
	}
	service, _ := testService(t, db)
	got, err := service.RejectWhiteListTopUpOrder(context.Background(), RejectWhiteListTopUpOrderCommand{
		OrderID: "whitelist-topup-order-1", IdempotencyKey: "reject-request-1",
		Actor: "owner", Channel: "panel",
	})
	if err != nil {
		t.Fatalf("RejectWhiteListTopUpOrder: %v", err)
	}
	if got.OrderID != "whitelist-topup-order-1" || got.PaymentState != PaymentCanceled {
		t.Fatalf("rejected order=%#v", got)
	}
	joined := strings.ToLower(statementsText(db.requestCalls[0].statements))
	for _, forbidden := range []string{
		"insert into payments", "insert into whitelist_balance_entries",
		"insert into whitelist_publication_controls",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("rejection contains forbidden mutation %q: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "insert into whitelist_topup_results") ||
		!strings.Contains(joined, "'rejected'") {
		t.Fatalf("rejection result is not durable: %s", joined)
	}
}

func TestConfirmWhiteListTopUpPaymentCreditsOnceAndEnablesPublication(t *testing.T) {
	period := whitelistbalance.Period{
		ID: "period-current", Ordinal: 0, StartsAtUnix: 1_999_000,
		EndsAtUnix: 2_086_400, IncludedGrantBytes: 0, AccessOrderID: "ordinary-access-order",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: "wl-ent-customer-1", CurrentPeriodID: period.ID, Version: 1,
	}
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(),
			whiteListTopUpOrderResult("payment-code-3", PaymentClaimed),
			resultsScript(rqlite.Result{Rows: []map[string]any{
				whiteListBalanceStateRow("wl-ent-customer-1", "active", 2_086_400, period, projection, 0),
			}}),
		},
		requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
			return make([]rqlite.Result, len(statements)), nil
		},
	}
	service, _ := testService(t, db)
	const paymentReference = "manual-bank-reference-secret"
	got, err := service.ConfirmWhiteListTopUpPayment(context.Background(), ConfirmWhiteListTopUpPaymentCommand{
		OrderID: "whitelist-topup-order-1", IdempotencyKey: "confirm-request-1",
		PaymentReference: paymentReference, Provider: "manual", Actor: "owner", Channel: "panel",
	})
	if err != nil {
		t.Fatalf("ConfirmWhiteListTopUpPayment: %v", err)
	}
	if got.OrderID != "whitelist-topup-order-1" || got.PeriodID != period.ID ||
		got.PurchasedBytes != 20000000000 || got.PurchasedRemainingBytes != 20000000000 ||
		!got.PublicationEnabled || got.OperationID == "" || got.PaymentID == "" ||
		got.BalanceEntryID == "" || got.ControlID == "" {
		t.Fatalf("confirmation=%#v", got)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction || db.requestCalls[0].level != rqlite.Linearizable {
		t.Fatalf("confirmation calls=%#v", db.requestCalls)
	}
	joined := strings.ToLower(statementsText(db.requestCalls[0].statements))
	for _, required := range []string{
		"'whitelist_topup_confirm'", "update orders set payment_state='confirmed'",
		"insert into payments", "insert into whitelist_balance_entries",
		"'purchased_credit'", "insert into whitelist_publication_controls",
		"'confirmed_gb_purchase'", "insert into whitelist_topup_results",
		"whitelist-topup-projection-rejected", "whitelist_renewal_intents",
		"update idempotency_requests set status='applied'",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("confirmation transaction missing %q: %s", required, joined)
		}
	}
	if strings.Index(joined, "update orders set payment_state='confirmed'") >=
		strings.Index(joined, "insert into payments") {
		t.Fatalf("payment must be inserted after the durable order decision: %s", joined)
	}
	for _, forbidden := range []string{
		"provisioning_state='applied'", "update customers set", "desired_node_state", "outbox_events",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("GB confirmation changed ordinary access through %q", forbidden)
		}
	}
	for _, statement := range db.requestCalls[0].statements {
		for _, argument := range statement.Args {
			if text, ok := argument.(string); ok && text == paymentReference {
				t.Fatal("raw payment reference was persisted")
			}
		}
	}
}

func TestConfirmWhiteListTopUpPaymentCreatesZeroGrantFirstPeriod(t *testing.T) {
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(),
			whiteListTopUpOrderResult("payment-code-4", PaymentClaimed),
			resultsScript(rqlite.Result{Rows: []map[string]any{{
				"entitlement_id": "wl-ent-customer-1", "customer_status": "active",
				"customer_expires_at_unix": int64(2_086_400), "commercial_debit_pending": int64(0),
				"renewal_intent_pending": int64(0),
			}}}),
			resultsScript(rqlite.Result{Rows: []map[string]any{{
				"access_order_id": "ordinary-access-order", "period_ends_at_unix": int64(2_086_400),
			}}}),
		},
		requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
			return make([]rqlite.Result, len(statements)), nil
		},
	}
	service, _ := testService(t, db)
	got, err := service.ConfirmWhiteListTopUpPayment(context.Background(), ConfirmWhiteListTopUpPaymentCommand{
		OrderID: "whitelist-topup-order-1", IdempotencyKey: "confirm-request-first-period",
		PaymentReference: "manual-reference-first-period", Provider: "manual", Actor: "owner",
	})
	if err != nil {
		t.Fatalf("ConfirmWhiteListTopUpPayment first period: %v", err)
	}
	if got.PeriodID == "" || got.PurchasedRemainingBytes != 20000000000 || got.BalanceVersion != 2 {
		t.Fatalf("first-period confirmation=%#v", got)
	}
	joined := strings.ToLower(statementsText(db.requestCalls[0].statements))
	if !strings.Contains(joined, "insert into whitelist_billing_periods") ||
		!strings.Contains(joined, "included_grant_bytes,access_order_id") {
		t.Fatalf("zero-grant period was not created: %s", joined)
	}
	if strings.Count(joined, "insert into whitelist_balance_entries") != 1 ||
		strings.Contains(joined, "'included_grant'") {
		t.Fatalf("zero grant created an included-credit journal entry: %s", joined)
	}
}

func TestConfirmWhiteListTopUpPaymentDefersWhileRenewalIntentPending(t *testing.T) {
	period := whitelistbalance.Period{
		ID: "period-current", Ordinal: 0, StartsAtUnix: 1_999_000,
		EndsAtUnix: 2_086_400, AccessOrderID: "ordinary-access-order",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: "wl-ent-customer-1", CurrentPeriodID: period.ID, Version: 1,
	}
	balanceRow := whiteListBalanceStateRow(
		"wl-ent-customer-1", "active", 2_086_400, period, projection, 0,
	)
	balanceRow["renewal_intent_pending"] = int64(1)
	db := &recordingRQLite{linear: []scriptedResult{
		resultsScript(),
		whiteListTopUpOrderResult("payment-code-renewal-pending", PaymentClaimed),
		rowsScript(balanceRow),
	}}
	service, _ := testService(t, db)
	_, err := service.ConfirmWhiteListTopUpPayment(context.Background(), ConfirmWhiteListTopUpPaymentCommand{
		OrderID: "whitelist-topup-order-1", IdempotencyKey: "confirm-renewal-pending",
		PaymentReference: "manual-reference-renewal-pending", Provider: "manual", Actor: "owner",
	})
	if !errors.Is(err, ErrUnavailable) || len(db.requestCalls) != 0 {
		t.Fatalf("renewal-pending top-up error=%v requests=%d", err, len(db.requestCalls))
	}
}

func TestConfirmWhiteListTopUpPaymentClassifiesLateRenewalGateAsUnavailable(t *testing.T) {
	period := whitelistbalance.Period{
		ID: "period-current", Ordinal: 0, StartsAtUnix: 1_999_000,
		EndsAtUnix: 2_086_400, AccessOrderID: "ordinary-access-order",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: "wl-ent-customer-1", CurrentPeriodID: period.ID, Version: 1,
	}
	loadedRow := whiteListBalanceStateRow(
		"wl-ent-customer-1", "active", 2_086_400, period, projection, 0,
	)
	pendingRow := whiteListBalanceStateRow(
		"wl-ent-customer-1", "active", 2_086_400, period, projection, 0,
	)
	pendingRow["renewal_intent_pending"] = int64(1)
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(),
			whiteListTopUpOrderResult("payment-code-late-renewal", PaymentClaimed),
			rowsScript(loadedRow),
			resultsScript(),
			rowsScript(pendingRow),
		},
		requestFn: func([]rqlite.Statement) ([]rqlite.Result, error) {
			return nil, errors.New("synthetic late renewal transaction abort")
		},
	}
	service, _ := testService(t, db)
	_, err := service.ConfirmWhiteListTopUpPayment(context.Background(), ConfirmWhiteListTopUpPaymentCommand{
		OrderID: "whitelist-topup-order-1", IdempotencyKey: "confirm-late-renewal",
		PaymentReference: "manual-reference-late-renewal", Provider: "manual", Actor: "owner",
	})
	if !errors.Is(err, ErrUnavailable) || len(db.requestCalls) != 1 {
		t.Fatalf("late renewal gate error=%v requests=%d", err, len(db.requestCalls))
	}
}

func TestSetWhiteListPublicationEnablesOnlyWithUsableBalance(t *testing.T) {
	period := whitelistbalance.Period{
		ID: "period-current", Ordinal: 0, StartsAtUnix: 1_999_000,
		EndsAtUnix: 2_086_400, IncludedGrantBytes: 0, AccessOrderID: "ordinary-access-order",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: "wl-ent-customer-1", CurrentPeriodID: period.ID,
		PurchasedRemainingBytes: 5000000000, Version: 2,
	}
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(),
			whiteListPublicationResult(1, false, "DEFAULT_OFF"),
			resultsScript(rqlite.Result{Rows: []map[string]any{
				whiteListBalanceStateRow("wl-ent-customer-1", "active", 2_086_400, period, projection, 0),
			}}),
		},
		requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
			return make([]rqlite.Result, len(statements)), nil
		},
	}
	service, _ := testService(t, db)
	got, err := service.SetWhiteListPublication(context.Background(), SetWhiteListPublicationCommand{
		EntitlementID: "wl-ent-customer-1", Enabled: true,
		IdempotencyKey: "admin-enable-1", Actor: "owner", Channel: "panel",
	})
	if err != nil {
		t.Fatalf("SetWhiteListPublication enable: %v", err)
	}
	if !got.Enabled || got.Version != 2 || got.Source != "ADMIN_ENABLE" || got.ControlID == "" {
		t.Fatalf("enabled publication=%#v", got)
	}
	joined := strings.ToLower(statementsText(db.requestCalls[0].statements))
	sourceBound := false
	for _, statement := range db.requestCalls[0].statements {
		for _, argument := range statement.Args {
			if argument == "ADMIN_ENABLE" {
				sourceBound = true
			}
		}
	}
	if !strings.Contains(joined, "insert into whitelist_publication_controls") || !sourceBound {
		t.Fatalf("admin enable was not persisted: %s", joined)
	}
	for _, forbidden := range []string{
		"insert into whitelist_balance_entries", "update whitelist_balance_projections",
		"delete from whitelist_balance",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("publication toggle mutated balance through %q", forbidden)
		}
	}
}

func TestSetWhiteListPublicationDisablePreservesBalanceWithoutBalanceRead(t *testing.T) {
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(),
			whiteListPublicationResult(2, true, "CONFIRMED_GB_PURCHASE"),
		},
		requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
			return make([]rqlite.Result, len(statements)), nil
		},
	}
	service, _ := testService(t, db)
	got, err := service.SetWhiteListPublication(context.Background(), SetWhiteListPublicationCommand{
		EntitlementID: "wl-ent-customer-1", Enabled: false,
		IdempotencyKey: "admin-disable-1", Actor: "owner", Channel: "panel",
	})
	if err != nil {
		t.Fatalf("SetWhiteListPublication disable: %v", err)
	}
	if got.Enabled || got.Version != 3 || got.Source != "ADMIN_DISABLE" {
		t.Fatalf("disabled publication=%#v", got)
	}
	if len(db.linearCalls) != 2 {
		t.Fatalf("disable performed %d reads, want idempotency plus current control only", len(db.linearCalls))
	}
	joined := strings.ToLower(statementsText(db.requestCalls[0].statements))
	if strings.Contains(joined, "whitelist_balance_entries") || strings.Contains(joined, "whitelist_balance_projections set") {
		t.Fatalf("disable mutated commercial history: %s", joined)
	}
}

func TestSetWhiteListPublicationNoOpPersistsIdempotentDecision(t *testing.T) {
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(),
			whiteListPublicationResult(1, false, "DEFAULT_OFF"),
		},
		requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
			return make([]rqlite.Result, len(statements)), nil
		},
	}
	service, _ := testService(t, db)
	got, err := service.SetWhiteListPublication(context.Background(), SetWhiteListPublicationCommand{
		EntitlementID: "wl-ent-customer-1", Enabled: false,
		IdempotencyKey: "admin-disable-noop-1", Actor: "owner", Channel: "panel",
	})
	if err != nil {
		t.Fatalf("SetWhiteListPublication no-op disable: %v", err)
	}
	if got.Enabled || got.Version != 2 || got.Source != "ADMIN_DISABLE" || got.ControlID == "" {
		t.Fatalf("persisted no-op publication=%#v", got)
	}
	if len(db.requestCalls) != 1 {
		t.Fatalf("no-op publication performed %d writes, want one idempotent decision", len(db.requestCalls))
	}
}

func whiteListTopUpPreparedResult(status string, expiresAtUnix int64) scriptedResult {
	return resultsScript(rqlite.Result{Rows: []map[string]any{{
		"entitlement_id": "wl-ent-customer-1", "customer_id": "customer-1",
		"customer_status": status, "customer_expires_at_unix": expiresAtUnix,
		"product_id": "wl-gb-20-v1", "amount_minor": int64(30000),
		"currency": "RUB", "bytes": int64(20000000000), "unit": "GB_DECIMAL",
		"kind": "WHITELIST_BYTES",
	}}})
}

func whiteListTopUpOrderResult(paymentCode string, state PaymentState) scriptedResult {
	return resultsScript(rqlite.Result{Rows: []map[string]any{{
		"order_id": "whitelist-topup-order-1", "payment_code": paymentCode,
		"entitlement_id": "wl-ent-customer-1", "product_id": "wl-gb-20-v1",
		"amount_minor": int64(30000), "currency": "RUB", "bytes": int64(20000000000),
		"payment_state": string(state), "expires_at_unix": int64(2_086_400),
		"origin_bot_id": "", "origin_chat_key_hmac": nil,
		"kind": "WHITELIST_BYTES", "unit": "GB_DECIMAL",
	}}})
}

func whiteListTopUpOrderResultWithOrigin(paymentCode string, state PaymentState) scriptedResult {
	result := whiteListTopUpOrderResult(paymentCode, state)
	result.results[0].Rows[0]["origin_bot_id"] = "maestro"
	result.results[0].Rows[0]["origin_chat_key_hmac"] = "chat-hmac"
	return result
}

func whiteListPublicationResult(version int64, enabled bool, source string) scriptedResult {
	enabledInt := int64(0)
	if enabled {
		enabledInt = 1
	}
	return resultsScript(rqlite.Result{Rows: []map[string]any{{
		"control_id": "publication-control-current", "entitlement_id": "wl-ent-customer-1",
		"version": version, "enabled": enabledInt, "source": source,
	}}})
}
