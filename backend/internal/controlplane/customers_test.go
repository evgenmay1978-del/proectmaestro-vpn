package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestCustomerByTokenUsesHMACAndLinearizableRead(t *testing.T) {
	const token = "raw-private-subscription-token"
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(map[string]any{
		"customer_id":    "customer-1",
		"status":         "active",
		"expires_at_unix": 2_100_000,
		"generation":     7,
	})}}
	service, secrets := testService(t, db)
	customer, err := service.CustomerByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("CustomerByToken: %v", err)
	}
	if customer.ID != "customer-1" || customer.Generation != 7 {
		t.Fatalf("customer = %#v", customer)
	}
	if len(db.linearCalls) != 1 || len(db.linearCalls[0].statements) != 1 {
		t.Fatalf("linearizable calls = %#v", db.linearCalls)
	}
	args := db.linearCalls[0].statements[0].Args
	wantHMAC := secrets.LookupHMAC("subscription-token", []byte(token))
	if fmt.Sprint(args) != fmt.Sprint([]any{wantHMAC}) {
		t.Fatalf("query args = %#v, want token HMAC only", args)
	}
}

func TestCustomerByTokenNeverSendsPlainTokenToSQLOrError(t *testing.T) {
	const token = "never-render-this-private-token"
	db := &recordingRQLite{linear: []scriptedResult{{err: errors.New("upstream included " + token)}}}
	service, _ := testService(t, db)
	_, err := service.CustomerByToken(context.Background(), token)
	if err == nil {
		t.Fatal("CustomerByToken unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked raw token: %v", err)
	}
	for _, call := range db.linearCalls {
		for _, statement := range call.statements {
			if strings.Contains(statement.SQL, token) || strings.Contains(fmt.Sprint(statement.Args), token) {
				t.Fatal("raw token reached SQL")
			}
		}
	}
}

func TestClaimDeviceAtomicallyEnforcesLimitAndStoresOnlyHMAC(t *testing.T) {
	const rawDevice = "raw-device-identity"
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{Rows: []map[string]any{{"device_id": "device-1"}}},
	)}}
	service, _ := testService(t, db)
	claim, err := service.ClaimDevice(context.Background(), "customer-1", rawDevice, "android", 3)
	if err != nil {
		t.Fatalf("ClaimDevice: %v", err)
	}
	if claim.DeviceID != "device-1" {
		t.Fatalf("claim = %#v", claim)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatal("device claim was not one transaction")
	}
	assertBackupDirtyImmediatelyAfter(t, db.requestCalls[0].statements, 0)
	joined := statementsText(db.requestCalls[0].statements)
	if !strings.Contains(joined, "COUNT") || !strings.Contains(joined, "ON CONFLICT") ||
		!strings.Contains(joined, "audit_events") {
		t.Fatalf("device limit/idempotency/audit missing: %s", joined)
	}
	for _, statement := range db.requestCalls[0].statements {
		if strings.Contains(statement.SQL, rawDevice) || strings.Contains(fmt.Sprint(statement.Args), rawDevice) {
			t.Fatal("raw device identity reached SQL")
		}
	}
	auditSQL := db.requestCalls[0].statements[2].SQL
	if !strings.Contains(auditSQL, "ON CONFLICT(event_id) DO NOTHING") {
		t.Fatalf("device claim audit is not retry-idempotent: %s", auditSQL)
	}
}

func TestClaimDeviceNoopReplayAndLimitRejectionCannotMarkDirty(t *testing.T) {
	tests := []struct {
		name      string
		finalRows []map[string]any
		wantErr   error
	}{
		{name: "exact idempotent replay", finalRows: []map[string]any{{"device_id": "device-1"}}},
		{name: "limit rejection", wantErr: ErrDeviceLimit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{requests: []scriptedResult{resultsScript(
				rqlite.Result{RowsAffected: 0}, rqlite.Result{}, rqlite.Result{},
				rqlite.Result{Rows: test.finalRows},
			)}}
			service, _ := testService(t, db)
			claim, err := service.ClaimDevice(context.Background(), "customer-1", "same-device", "android", 3)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ClaimDevice error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && claim.DeviceID != "device-1" {
				t.Fatalf("idempotent replay claim = %#v", claim)
			}
			statements := db.requestCalls[0].statements
			assertBackupDirtyImmediatelyAfter(t, statements, 0)
			authoritativeSQL := strings.ToLower(statements[0].SQL)
			if !strings.Contains(authoritativeSQL, "do update set") || !strings.Contains(authoritativeSQL, "where devices.") {
				t.Fatalf("exact no-op replay is not suppressed by authoritative SQL: %s", statements[0].SQL)
			}
		})
	}
}

func TestConcurrentSameDeviceClaimIsIdempotent(t *testing.T) {
	db := &recordingRQLite{}
	db.requestFn = func(_ []rqlite.Statement) ([]rqlite.Result, error) {
		return []rqlite.Result{
			{RowsAffected: 1},
			{RowsAffected: 1},
			{RowsAffected: 1},
			{Rows: []map[string]any{{"device_id": "same-device"}}},
		}, nil
	}
	service, _ := testService(t, db)

	const workers = 50
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	ids := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, err := service.ClaimDevice(context.Background(), "customer-1", "same-raw-device", "android", 3)
			if err != nil {
				errs <- err
				return
			}
			ids <- claim.DeviceID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Fatalf("ClaimDevice: %v", err)
	}
	for id := range ids {
		if id != "same-device" {
			t.Fatalf("concurrent claim returned %q", id)
		}
	}
	for _, call := range db.requestCalls {
		if !strings.Contains(statementsText(call.statements), "ON CONFLICT") {
			t.Fatal("concurrent claim lacks database idempotency")
		}
	}
}
