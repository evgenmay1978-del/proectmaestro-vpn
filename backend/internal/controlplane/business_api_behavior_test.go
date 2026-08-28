package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"golang.org/x/crypto/bcrypt"
)

func TestBusinessCustomerReadDecryptsAccessWithoutPersistedCiphertextLeakage(t *testing.T) {
	db := &recordingRQLite{}
	service, box := testService(t, db)
	tokenEnvelope, err := box.Seal(SecretScope{OwnerType: "customer", OwnerID: "customer-1", Field: "token", Kind: "subscription"}, []byte("raw-token"))
	if err != nil {
		t.Fatal(err)
	}
	credentialEnvelope, err := box.Seal(SecretScope{OwnerType: "customer", OwnerID: "customer-1", Field: "credential", Kind: "vless"}, []byte("raw-credential"))
	if err != nil {
		t.Fatal(err)
	}
	db.linear = []scriptedResult{
		resultsScript(rqlite.Result{Rows: []map[string]any{{"customer_id": "customer-1", "status": "active", "expires_at_unix": int64(2_100_000), "generation": int64(7)}}}),
		resultsScript(rqlite.Result{Rows: []map[string]any{{"display_login": "alice"}}}),
		resultsScript(rqlite.Result{Rows: []map[string]any{{"protocol": "vless", "token_envelope": encodedEnvelope(t, tokenEnvelope), "secret_envelope": encodedEnvelope(t, credentialEnvelope)}}}),
	}

	got, err := service.BusinessCustomerByLogin(context.Background(), "Alice")
	if err != nil {
		t.Fatalf("BusinessCustomerByLogin: %v", err)
	}
	if got.Login != "alice" || got.Access.SubscriptionToken != "raw-token" || got.Access.Credentials["vless"] != "raw-credential" {
		t.Fatalf("decrypted view=%#v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), encodedEnvelope(t, tokenEnvelope)) || strings.Contains(string(raw), encodedEnvelope(t, credentialEnvelope)) {
		t.Fatalf("view leaked ciphertext: %s", raw)
	}
}

func TestBusinessSettingReadReturnsPublicMembersAndOnlySecretPresence(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"public_value_json": `{"room":"r1","provider":"telemost"}`, "generation": int64(4)}}},
		rqlite.Result{Rows: []map[string]any{{"member_key": "alice", "member_value_json": `true`}}},
		rqlite.Result{Rows: []map[string]any{{"configured": int64(1)}}},
	)}}
	service, _ := testService(t, db)
	setting, err := service.ReadBusinessSetting(context.Background(), "olcrtc")
	if err != nil {
		t.Fatalf("ReadBusinessSetting: %v", err)
	}
	if setting.Generation != 4 || string(setting.PublicValueJSON) != `{"room":"r1","provider":"telemost"}` || string(setting.Members["alice"]) != "true" || !setting.SecretConfigured {
		t.Fatalf("setting=%#v", setting)
	}
	if _, ok := any(setting).(interface{ Secret() string }); ok {
		t.Fatal("setting read exposed a secret accessor")
	}
}

func TestBusinessPasswordVerifyAndChangeUsesEncryptedAtomicIdempotentTransaction(t *testing.T) {
	db := &recordingRQLite{}
	service, box := testService(t, db)
	currentHash, err := bcrypt.GenerateFromPassword([]byte("current-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := box.Seal(SecretScope{OwnerType: "principal", OwnerID: "owner-1", Field: "password", Kind: "bcrypt"}, currentHash)
	if err != nil {
		t.Fatal(err)
	}
	db.linear = []scriptedResult{
		resultsScript(rqlite.Result{}),
		resultsScript(rqlite.Result{Rows: []map[string]any{{"verifier_envelope": encodedEnvelope(t, envelope)}}}),
	}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		joined := strings.ToLower(joinSQL(statements))
		for _, required := range []string{"idempotency_requests", "principal_credentials", "revocation_epoch", "web_sessions"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("password transaction missing %s: %s", required, joined)
			}
		}
		for _, statement := range statements {
			for _, arg := range statement.Args {
				if value, ok := arg.(string); ok && (strings.Contains(value, "current-password") || strings.Contains(value, "replacement-password")) {
					t.Fatalf("plaintext password reached SQL args: %#v", statements)
				}
			}
		}
		results := make([]rqlite.Result, len(statements))
		results[len(results)-1].Rows = []map[string]any{{"operation_id": "password-change-1"}}
		return results, nil
	}
	if err := service.ChangePrincipalPassword(context.Background(), "owner-1", "current-password", "replacement-password", "idem-password-1"); err != nil {
		t.Fatalf("ChangePrincipalPassword: %v", err)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction || db.requestCalls[0].level != rqlite.Linearizable {
		t.Fatalf("password change was not one linearizable transaction: %#v", db.requestCalls)
	}
}

func TestBusinessAdminReadsUseLinearizableRQLiteAndFailClosed(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{
		resultsScript(rqlite.Result{Rows: []map[string]any{{"voters": int64(3), "enabled": int64(2)}}}),
		resultsScript(rqlite.Result{Rows: []map[string]any{{"event_id": "audit-1", "action": "customer.enable", "created_at_unix": int64(2_000_000)}}}),
		{err: errors.New("quorum unavailable")},
	}}
	service, _ := testService(t, db)
	ready, quorum, err := service.BusinessClusterStatus(context.Background())
	if err != nil || !ready || !quorum {
		t.Fatalf("cluster status ready=%v quorum=%v error=%v", ready, quorum, err)
	}
	events, err := service.RecentBusinessAudit(context.Background(), 10)
	if err != nil || len(events) != 1 || events[0].Action != "customer.enable" || !events[0].CreatedAt.Equal(time.Unix(2_000_000, 0)) {
		t.Fatalf("audit=%#v error=%v", events, err)
	}
	if _, _, err := service.BusinessClusterStatus(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable cluster error=%v", err)
	}
	for _, call := range db.linearCalls {
		if call.level != rqlite.Linearizable {
			t.Fatalf("admin read was not linearizable: %#v", call)
		}
	}
}

func joinSQL(statements []rqlite.Statement) string {
	parts := make([]string, 0, len(statements))
	for _, statement := range statements {
		parts = append(parts, statement.SQL)
	}
	return strings.Join(parts, "\n")
}
