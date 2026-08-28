package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestTask8SecretSettingInsertMatchesFiveColumns(t *testing.T) {
	update := SettingUpdate{
		Key: "wbstream", ExpectedGeneration: 2, PublicValueJSON: `{}`,
		Secret: &Envelope{KeyVersion: 1}, Actor: "owner",
		CommandType: "setting.wbstream.token", IdempotencyKey: "idem-wb-1",
		RequestFingerprint: "protected-request-fingerprint",
	}
	requestHash, err := settingRequestHash(update)
	if err != nil {
		t.Fatalf("settingRequestHash: %v", err)
	}
	db := &recordingRQLite{
		linear: []scriptedResult{resultsScript(rqlite.Result{})},
		requests: []scriptedResult{resultsScript(
			rqlite.Result{},
			rqlite.Result{Rows: []map[string]any{{
				"request_hash": requestHash, "status": "applied", "response_json": `{"generation":3}`,
			}}},
		)},
	}
	service, _ := testService(t, db)
	if _, err := service.UpdateSetting(context.Background(), update); err != nil {
		t.Fatalf("UpdateSetting: %v", err)
	}
	joined := strings.ToLower(joinedRequestSQL(db))
	if !strings.Contains(joined, "insert into setting_secrets") {
		t.Fatalf("secret insert missing: %s", joined)
	}
	if strings.Contains(joined, "select ?,?,?,?,?,? where") {
		t.Fatalf("secret insert has six values for five columns: %s", joined)
	}
}
