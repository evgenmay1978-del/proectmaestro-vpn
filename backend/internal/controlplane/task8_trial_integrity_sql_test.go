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

func seedIntegrityLegacyOnlyTrial(t *testing.T, db *customerIntegritySQLite, service *Service, source, kind, value string) {
	t.Helper()
	salt := []byte("task8-synthetic-legacy-only-salt")
	rows := db.must(t, rqlite.Statement{SQL: `SELECT secret_id FROM imported_secrets WHERE secret_id='legacy-trial-salt-v1'`})
	if len(rows[0].Rows) == 0 {
		envelope, err := service.store.secrets.Seal(SecretScope{OwnerType: "trial_lookup", OwnerID: "legacy", Field: "salt", Kind: "hmac-key"}, salt)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(map[string]any{"key_version": envelope.KeyVersion, "nonce_b64": envelope.Nonce, "ciphertext_b64": envelope.Ciphertext})
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(salt)
		db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_secrets(secret_id,owner_type,owner_source_key,field,kind,key_version,secret_envelope,secret_sha256,imported_at_unix)
VALUES ('legacy-trial-salt-v1','trial_lookup','legacy','salt','hmac-key',?,?,?,1)`, Args: []any{envelope.KeyVersion, encoded, hex.EncodeToString(digest[:])}})
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_legacy_trial_uses(source_key,hash_kind,legacy_hmac,lookup_secret_id,imported_at_unix)
VALUES (?,?,?,'legacy-trial-salt-v1',1)`, Args: []any{source, kind, integrityHMAC(salt, value)}})
}

func trialIntegritySnapshot(t *testing.T, db *customerIntegritySQLite) []rqlite.Result {
	t.Helper()
	return append(db.snapshot(t), db.must(t,
		rqlite.Statement{SQL: `SELECT * FROM imported_legacy_trial_uses ORDER BY source_key`},
		rqlite.Statement{SQL: `SELECT * FROM import_runs ORDER BY import_run_id`},
		rqlite.Statement{SQL: `SELECT * FROM import_batches ORDER BY import_run_id,batch_index`},
	)...)
}

func seedIntegrityTrialImport(t *testing.T, db *customerIntegritySQLite, status string) {
	t.Helper()
	var target, completed any
	if status == "applied" {
		target, completed = strings.Repeat("3", 64), int64(2)
	}
	statements := []rqlite.Statement{{SQL: `INSERT INTO import_runs(import_run_id,snapshot_kind,source_sha256,plan_sha256,target_sha256,batch_count,status,started_at_unix,completed_at_unix)
VALUES ('trial-import','full',?,?,?,2,?,1,?)`, Args: []any{strings.Repeat("1", 64), strings.Repeat("2", 64), target, status, completed}}}
	if status == "applied" {
		statements = append(statements,
			rqlite.Statement{SQL: `INSERT INTO import_batches VALUES ('trial-import',0,?,1,'applied',1)`, Args: []any{strings.Repeat("4", 64)}},
			rqlite.Statement{SQL: `INSERT INTO import_batches VALUES ('trial-import',1,?,1,'applied',2)`, Args: []any{strings.Repeat("5", 64)}},
		)
	}
	db.must(t, statements...)
}

func TestTask8LegacyOnlyTrialsStayUsedAndKeepHashKindsSQLite(t *testing.T) {
	for _, test := range []struct {
		kind, value string
		denied      bool
	}{
		{"anchor", "used-anchor", true},
		{"drm", "drm:used-device", true},
		{"drm", "used-anchor", false},
		{"anchor", "drm:used-device", false},
	} {
		t.Run(test.kind+"/"+test.value, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			seedIntegrityLegacyOnlyTrial(t, db, service, "used-source", test.kind, test.value)
			before := trialIntegritySnapshot(t, db)
			command := RedeemTrialCommand{Login: "Candidate", Anchor: "used-anchor", DRMIdentity: "used-device", Days: 7, IdempotencyKey: "candidate"}
			first, err := service.RedeemTrial(context.Background(), command)
			if test.denied {
				if err == nil || !reflect.DeepEqual(before, trialIntegritySnapshot(t, db)) {
					t.Fatal("permanently used legacy identity was accepted or mutated durable state")
				}
			} else {
				if err != nil {
					t.Fatalf("unrelated typed hash blocked fresh trial: %v", err)
				}
				replay, err := service.RedeemTrial(context.Background(), command)
				if err != nil || !reflect.DeepEqual(first, replay) {
					t.Fatal("fresh trial did not replay its exact committed result")
				}
			}
			if len(db.must(t, rqlite.Statement{SQL: `SELECT source_key FROM imported_trial_identities`})[0].Rows) != 0 {
				t.Fatal("legacy-only use invented a dual/current identity")
			}
		})
	}
}

func TestTask8LegacyOnlyTrialStorageIsImmutableSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	seedIntegrityLegacyOnlyTrial(t, db, service, "used-source", "anchor", "used-anchor")
	// The same opaque hash may occur in the other ledger, but not twice in one kind.
	db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_legacy_trial_uses SELECT 'other-kind','drm',legacy_hmac,lookup_secret_id,imported_at_unix FROM imported_legacy_trial_uses WHERE source_key='used-source'`})
	db.must(t, rqlite.Statement{SQL: `UPDATE imported_legacy_trial_uses SET legacy_hmac=legacy_hmac WHERE source_key='used-source'`})
	db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_secrets(secret_id,owner_type,owner_source_key,field,kind,key_version,secret_envelope,secret_sha256,imported_at_unix)
VALUES ('wrong-salt','setting','legacy','salt','hmac-key',1,'opaque',?,1)`, Args: []any{strings.Repeat("a", 64)}})
	before := trialIntegritySnapshot(t, db)
	for _, sql := range []string{
		`UPDATE imported_legacy_trial_uses SET hash_kind='drm' WHERE source_key='used-source'`,
		`UPDATE imported_legacy_trial_uses SET legacy_hmac=printf('%064d',0) WHERE source_key='used-source'`,
		`UPDATE imported_legacy_trial_uses SET source_key='changed' WHERE source_key='used-source'`,
		`UPDATE imported_legacy_trial_uses SET lookup_secret_id='wrong-salt' WHERE source_key='used-source'`,
		`UPDATE imported_legacy_trial_uses SET imported_at_unix=2 WHERE source_key='used-source'`,
		`DELETE FROM imported_legacy_trial_uses WHERE source_key='used-source'`,
		`INSERT INTO imported_legacy_trial_uses SELECT 'duplicate',hash_kind,legacy_hmac,lookup_secret_id,imported_at_unix FROM imported_legacy_trial_uses WHERE source_key='used-source'`,
		`INSERT INTO imported_legacy_trial_uses SELECT 'wrong-owner','anchor',printf('%064d',0),'wrong-salt',1`,
		`INSERT INTO imported_legacy_trial_uses SELECT 'unknown-salt','anchor',printf('%064d',0),'absent',1`,
		`INSERT INTO imported_legacy_trial_uses SELECT 'bad-hash','anchor',upper(legacy_hmac),lookup_secret_id,1 FROM imported_legacy_trial_uses WHERE source_key='used-source'`,
		`INSERT INTO imported_legacy_trial_uses SELECT 'bad-kind','unknown',legacy_hmac,lookup_secret_id,1 FROM imported_legacy_trial_uses WHERE source_key='used-source'`,
		`INSERT OR REPLACE INTO imported_legacy_trial_uses SELECT source_key,hash_kind,printf('%064d',0),lookup_secret_id,imported_at_unix FROM imported_legacy_trial_uses WHERE source_key='used-source'`,
		`INSERT INTO imported_trial_identities SELECT source_key,legacy_hmac,printf('%064d',0),1,0,lookup_secret_id,1 FROM imported_legacy_trial_uses WHERE source_key='used-source'`,
	} {
		if _, err := db.execute(context.Background(), true, rqlite.Statement{SQL: sql}); err == nil {
			t.Fatalf("immutable legacy-use constraint accepted: %s", sql)
		}
		if !reflect.DeepEqual(before, trialIntegritySnapshot(t, db)) {
			t.Fatal("rejected legacy-use statement changed durable state")
		}
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_trial_identities VALUES ('old-dual',printf('%064d',1),printf('%064d',2),1,0,'legacy-trial-salt-v1',1)`})
	if _, err := db.execute(context.Background(), true, rqlite.Statement{SQL: `INSERT INTO imported_legacy_trial_uses VALUES ('old-dual','anchor',printf('%064d',3),'legacy-trial-salt-v1',1)`}); err == nil {
		t.Fatal("existing dual source was replaced by a legacy-only identity")
	}
}

func TestTask8TrialDeniedDuringUnresolvedImportSQLite(t *testing.T) {
	for _, status := range []string{"applying", "failed"} {
		t.Run(status, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			seedIntegrityLegacyOnlyTrial(t, db, service, "unrelated", "anchor", "other-anchor")
			seedIntegrityTrialImport(t, db, status)
			// One committed batch cannot authorize redemption before the rest of the ledger arrives.
			db.must(t, rqlite.Statement{SQL: `INSERT INTO import_batches VALUES ('trial-import',0,?,1,'applied',1)`, Args: []any{strings.Repeat("4", 64)}})
			before := trialIntegritySnapshot(t, db)
			_, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Fresh", Anchor: "fresh-anchor", Days: 7, IdempotencyKey: "fresh"})
			if err == nil || !reflect.DeepEqual(before, trialIntegritySnapshot(t, db)) {
				t.Fatal("unresolved partial import permitted a trial or mutated customer state")
			}
		})
	}
}

func TestTask8TrialImportAfterSaltReadCannotBypassGateSQLite(t *testing.T) {
	for _, test := range []struct {
		name        string
		knownSalt   bool
		status      string
		matchingUse bool
	}{
		{"import-starts-after-read", true, "applying", false},
		{"unknown-salt-even-after-completion", false, "applied", false},
		{"matching-use-arrives-after-read", true, "applied", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			if test.knownSalt {
				seedIntegrityLegacyOnlyTrial(t, db, service, "known", "anchor", "other-anchor")
			}
			var before []rqlite.Result
			db.beforeRequest = func() {
				if !test.knownSalt {
					seedIntegrityLegacyOnlyTrial(t, db, service, "new-salt", "anchor", "other-anchor")
				}
				if test.matchingUse {
					seedIntegrityLegacyOnlyTrial(t, db, service, "concurrent-use", "anchor", "fresh-anchor")
				}
				seedIntegrityTrialImport(t, db, test.status)
				before = trialIntegritySnapshot(t, db)
			}
			_, err := service.RedeemTrial(context.Background(), RedeemTrialCommand{Login: "Fresh", Anchor: "fresh-anchor", Days: 7, IdempotencyKey: "race"})
			if before == nil || err == nil || !reflect.DeepEqual(before, trialIntegritySnapshot(t, db)) {
				t.Fatal("import committed after the salt read bypassed the atomic trial claim")
			}
		})
	}
}

func TestTask8CompletedLegacyImportAllowsFreshTrialSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	seedIntegrityLegacyOnlyTrial(t, db, service, "old-anchor", "anchor", "other-anchor")
	seedIntegrityLegacyOnlyTrial(t, db, service, "old-drm", "drm", "drm:other-device")
	seedIntegrityTrialImport(t, db, "applied")
	command := RedeemTrialCommand{Login: "Fresh", Anchor: "fresh-anchor", DRMIdentity: "fresh-device", Days: 7, IdempotencyKey: "completed-import"}
	first, err := service.RedeemTrial(context.Background(), command)
	if err != nil {
		t.Fatalf("completed import blocked a fresh trial: %v", err)
	}
	replay, err := service.RedeemTrial(context.Background(), command)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatal("fresh trial after import did not replay exactly")
	}
}
