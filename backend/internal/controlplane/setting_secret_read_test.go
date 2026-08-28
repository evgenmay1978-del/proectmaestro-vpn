package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestReadSettingSecretDecryptsRQLiteEnvelope(t *testing.T) {
	db := &recordingRQLite{}
	service, box := testService(t, db)
	envelope, err := box.Seal(SecretScope{OwnerType: "setting", OwnerID: "wbstream", Field: "secret", Kind: "wbstream"}, []byte("account-token"))
	if err != nil { t.Fatal(err) }
	db.linear = []scriptedResult{resultsScript(rqlite.Result{Rows: []map[string]any{{"secret_envelope": encodedEnvelope(t, envelope)}}})}

	raw, err := service.ReadSettingSecret(context.Background(), "wbstream")
	if err != nil { t.Fatalf("ReadSettingSecret: %v", err) }
	if string(raw) != "account-token" { t.Fatalf("secret=%q", raw) }
	if len(db.linearCalls) != 1 || !strings.Contains(strings.ToLower(db.linearCalls[0].statements[0].SQL), "setting_secrets") {
		t.Fatalf("secret read calls=%#v", db.linearCalls)
	}
	if strings.Contains(strings.ToLower(db.linearCalls[0].statements[0].SQL), "account-token") {
		t.Fatal("plaintext token leaked into SQL")
	}
}
