package controlplane

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func integrityHMAC(key []byte, value string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func seedIntegrityImportedTrial(t *testing.T, db *customerIntegritySQLite, service *Service, kind, anchor, device string) {
	t.Helper()
	salt := []byte("task8-synthetic-legacy-trial-salt")
	envelope, err := service.store.secrets.Seal(SecretScope{OwnerType: "trial_lookup", OwnerID: "legacy", Field: "salt", Kind: "hmac-key"}, salt)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]any{"key_version": envelope.KeyVersion, "nonce_b64": envelope.Nonce, "ciphertext_b64": envelope.Ciphertext})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(salt)
	legacy, current := strings.Repeat("c", 64), strings.Repeat("d", 64)
	switch kind {
	case "current-anchor":
		current = integrityHMAC(bytes.Repeat([]byte{0x62}, 32), "trial-anchor\x00"+anchor)
	case "current-device":
		current = integrityHMAC(bytes.Repeat([]byte{0x62}, 32), "trial-device\x00"+device)
	case "legacy-anchor":
		legacy = integrityHMAC(salt, anchor)
	case "legacy-device":
		legacy = integrityHMAC(salt, "drm:"+device)
	default:
		t.Fatal("unknown imported trial fixture")
	}
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO imported_secrets(secret_id,owner_type,owner_source_key,field,kind,key_version,secret_envelope,secret_sha256,imported_at_unix) VALUES ('legacy-trial-salt-v1','trial_lookup','legacy','salt','hmac-key',1,?,?,1)`, Args: []any{encoded, hex.EncodeToString(digest[:])}},
		rqlite.Statement{SQL: `INSERT INTO imported_trial_identities(source_key,legacy_anchor_hmac,current_hmac,used,expires_at_unix,lookup_secret_id,imported_at_unix) VALUES ('imported-trial',?,?,1,1,'legacy-trial-salt-v1',1)`, Args: []any{legacy, current}},
	)
}

func TestTask8TrialDuplicateIdentityPreservesAllStateSQLite(t *testing.T) {
	for _, test := range []struct{ name, anchor, device, imported, login string }{
		{"anchor-existing", "used-anchor", "fresh-device", "", "Existing"},
		{"anchor-new", "used-anchor", "fresh-device", "", "New"},
		{"device-existing", "fresh-anchor", "used-device", "", "Existing"},
		{"device-new", "fresh-anchor", "used-device", "", "New"},
		{"current-anchor", "used-anchor", "used-device", "current-anchor", "Existing"},
		{"current-device", "used-anchor", "used-device", "current-device", "New"},
		{"legacy-anchor", "used-anchor", "used-device", "legacy-anchor", "Existing"},
		{"legacy-device", "used-anchor", "used-device", "legacy-device", "New"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			seedIntegrityCustomer(t, service)
			if test.imported != "" {
				seedIntegrityImportedTrial(t, db, service, test.imported, test.anchor, test.device)
			} else if _, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Winner", Anchor: "used-anchor", DRMIdentity: "used-device", Days: 7, IdempotencyKey: "winner"}); err != nil {
				t.Fatalf("seed trial: %v", err)
			}
			before := db.snapshot(t)
			_, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: test.login, Anchor: test.anchor, DRMIdentity: test.device, Days: 99, IdempotencyKey: "duplicate"})
			if err == nil {
				t.Error("duplicate identity accepted")
			}
			if !reflect.DeepEqual(before, db.snapshot(t)) {
				t.Error("duplicate identity changed durable customer/access/desired/outbox/idempotency state")
			}
		})
	}
}

func TestTask8TrialIdentityClaimAfterReadsIsAtomicSQLite(t *testing.T) {
	for _, kind := range []string{"anchor", "device", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			seedIntegrityCustomer(t, service)
			var before []rqlite.Result
			db.beforeRequest = func() {
				if kind == "legacy" {
					seedIntegrityImportedTrial(t, db, service, "legacy-anchor", "candidate-anchor", "candidate-device")
				} else {
					anchor, device := "candidate-anchor", "different-device"
					if kind == "device" {
						anchor, device = "different-anchor", "candidate-device"
					}
					if _, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Winner", Anchor: anchor, DRMIdentity: device, Days: 7, IdempotencyKey: "winner"}); err != nil {
						t.Fatalf("concurrent winner: %v", err)
					}
				}
				before = db.snapshot(t)
			}
			_, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Existing", Anchor: "candidate-anchor", DRMIdentity: "candidate-device", Days: 99, IdempotencyKey: "loser"})
			if before == nil {
				t.Fatal("candidate never reached its transaction")
			}
			if err == nil {
				t.Error("identity claimed after reads was accepted")
			}
			if !reflect.DeepEqual(before, db.snapshot(t)) {
				t.Error("losing trial mutated committed winner or existing customer")
			}
		})
	}
}

func TestTask8TrialCustomerGenerationRaceRollsBackSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	seedIntegrityCustomer(t, service)
	var winnerState []rqlite.Result
	db.beforeRequest = func() {
		if _, err := service.ExtendCustomer(context.Background(), ExtendCustomerCommand{Login: "Existing", Days: 1, IdempotencyKey: "concurrent-winner"}); err != nil {
			t.Fatalf("concurrent winner: %v", err)
		}
		winnerState = db.snapshot(t)
	}
	_, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Existing", Anchor: "candidate-anchor", DRMIdentity: "candidate-device", Days: 7, IdempotencyKey: "stale-trial"})
	if winnerState == nil {
		t.Fatal("candidate never reached its transaction")
	}
	if err == nil {
		t.Error("stale trial succeeded without applying its customer generation")
	}
	if !reflect.DeepEqual(winnerState, db.snapshot(t)) {
		t.Error("stale trial mutated the committed winner state")
	}
}
func TestTask8TrialRequiresOneRedemptionSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	seedIntegrityCustomer(t, service)
	db.must(t, rqlite.Statement{SQL: `CREATE TRIGGER suppress_trial BEFORE INSERT ON trial_redemptions BEGIN SELECT RAISE(IGNORE); END`})
	before := db.snapshot(t)
	_, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Existing", Anchor: "fresh-anchor", DRMIdentity: "fresh-device", Days: 7, IdempotencyKey: "suppressed"})
	if err == nil {
		t.Error("zero-row redemption accepted")
	}
	if !reflect.DeepEqual(before, db.snapshot(t)) {
		t.Error("zero-row redemption did not roll back the whole customer transaction")
	}
}

func TestTask8TrialWithoutDRMDoesNotShareIdentitySQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	for _, login := range []string{"First", "Second"} {
		command := RedeemTrialCommand{Login: login, Anchor: login + "-anchor", Days: 7, IdempotencyKey: login}
		first, err := service.RedeemTrial(context.Background(), command)
		if err != nil {
			t.Fatalf("trial without DRM: %v", err)
		}
		replayed, err := service.RedeemTrial(context.Background(), command)
		if err != nil || !reflect.DeepEqual(first, replayed) {
			t.Fatal("trial first response differs from committed replay")
		}
	}
	if len(db.must(t, rqlite.Statement{SQL: `SELECT redemption_id FROM trial_redemptions`})[0].Rows) != 2 {
		t.Fatal("distinct DRM-less devices did not each commit one redemption")
	}
}

func TestTask8TrialEmptyAnchorCannotBypassRedemptionSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	before := db.snapshot(t)
	_, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Empty", Days: 7, IdempotencyKey: "empty"})
	if err == nil || !reflect.DeepEqual(before, db.snapshot(t)) {
		t.Fatal("empty anchor bypassed trial identity protection")
	}
}

func TestTask8TrialDerivesLegacyDeviceFromAnchorSQLite(t *testing.T) {
	for _, imported := range []bool{false, true} {
		name := "canonical"
		if imported {
			name = "imported"
		}
		t.Run(name, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			seedIntegrityCustomer(t, service)
			const device = "0123456789abcdef"
			if imported {
				seedIntegrityImportedTrial(t, db, service, "legacy-device", "unrelated-anchor", device)
			} else if _, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Winner", Anchor: "winner|" + device + "|model", DRMIdentity: device, Days: 7, IdempotencyKey: "winner"}); err != nil {
				t.Fatal(err)
			}
			before := db.snapshot(t)
			_, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Existing", Anchor: "changed-ssaid|" + device + "|model", Days: 7, IdempotencyKey: "embedded-device"})
			if err == nil || !reflect.DeepEqual(before, db.snapshot(t)) {
				t.Fatal("legacy anchor's previously used DRM identity was not rejected atomically")
			}
		})
	}
}

func TestTask8TrialUnrelatedImportedIdentityRemainsEligibleSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	seedIntegrityImportedTrial(t, db, service, "legacy-anchor", "different-anchor", "different-device")
	command := RedeemTrialCommand{Login: "Fresh", Anchor: "fresh-anchor", DRMIdentity: "fresh-device", Days: 7, IdempotencyKey: "fresh"}
	first, err := service.RedeemTrial(context.Background(), command)
	if err != nil {
		t.Fatalf("unrelated imported identity blocked a fresh trial: %v", err)
	}
	replayed, err := service.RedeemTrial(context.Background(), command)
	if err != nil || !reflect.DeepEqual(first, replayed) {
		t.Fatal("fresh trial with imported history did not replay identically")
	}
}
