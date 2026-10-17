package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func runtimeDomainCipher(t *testing.T, box *SecretBox, key string, doc LegacyRuntimeSettingDocument) (string, string) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := box.Seal(SecretScope{OwnerType: "setting", OwnerID: key, Field: "secret", Kind: key}, raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(encoded), hex.EncodeToString(digest[:])
}

func seedRuntimeDomainMarker(t *testing.T, db *customerIntegritySQLite, key, capsule, digest, encoded string) {
	t.Helper()
	id := "runtime-setting-v1:" + key + ":" + capsule
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var envelope Envelope
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("fixture envelope")
	}
	source, _ := json.Marshal(map[string]any{"secret_id": id, "owner_type": "setting", "owner_source_key": key, "field": "secret", "kind": key, "key_version": envelope.KeyVersion, "nonce_b64": base64.StdEncoding.EncodeToString(envelope.Nonce), "ciphertext_b64": base64.StdEncoding.EncodeToString(envelope.Ciphertext), "sha256": digest})
	sum := sha256.Sum256(source)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_secrets(secret_id,owner_type,owner_source_key,field,kind,key_version,secret_envelope,secret_sha256,imported_at_unix) VALUES(?,'setting',?,'secret',?,?,?,?,1)`, Args: []any{id, key, key, envelope.KeyVersion, string(source), digest}}, rqlite.Statement{SQL: `INSERT INTO imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix) VALUES('encrypted_secret',?,?,?,'active',1)`, Args: []any{id, id, hex.EncodeToString(sum[:])}})
}

func TestLegacyRuntimeSettingReadsExactMembersAndRejectsDriftSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	seedExactLoginCustomer(t, db, service, "Alice")
	seedExactLoginCustomer(t, db, service, "alice")
	doc := LegacyRuntimeSettingDocument{SchemaVersion: 1, SettingKey: "olcrtc", CapsuleSHA256: strings.Repeat("a", 64), CustomersSHA256: strings.Repeat("b", 64), ConfigJSON: json.RawMessage(`{"enabled":true,"logins":["Alice","alice"]}`), Members: []LegacyRuntimeMember{}}
	for _, login := range []string{"Alice", "alice"} {
		identity, err := service.ResolveCustomerLogin(ctx, login)
		if err != nil {
			t.Fatal(err)
		}
		doc.Members = append(doc.Members, LegacyRuntimeMember{Login: login, CustomerSourceKey: identity.source, CustomerID: identity.CustomerID(), CustomerSHA256: identity.digest, LoginHMAC: identity.LookupHMAC(), MemberHMAC: identity.SettingMemberHMAC("olcrtc")})
	}
	encoded, digest := runtimeDomainCipher(t, service.store.secrets, "olcrtc", doc)
	seedRuntimeDomainMarker(t, db, "olcrtc", doc.CapsuleSHA256, digest, encoded)
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO cluster_settings(setting_key,public_value_json,generation,updated_at_unix) VALUES('olcrtc','{}',1,1)`},
		rqlite.Statement{SQL: `INSERT INTO setting_secrets(setting_key,secret_envelope,secret_sha256,key_version,updated_at_unix) VALUES('olcrtc',?,?,1,1)`, Args: []any{encoded, digest}},
	)
	for _, member := range doc.Members {
		db.must(t, rqlite.Statement{SQL: `INSERT INTO setting_members(setting_key,member_key,member_value_json,generation) VALUES('olcrtc',?,'{"enabled":true}',1)`, Args: []any{member.MemberHMAC}})
	}
	value, err := service.ReadLegacyRuntimeSetting(ctx, "olcrtc")
	if err != nil || len(value.Members()) != 2 || value.Members()[0].MemberHMAC == value.Members()[1].MemberHMAC {
		t.Fatal("exact case-sensitive members did not survive authenticated SQL read")
	}
	if _, err := AuthenticateLegacyRuntimeSetting(service.store.secrets, "vkturn", encoded, digest); !errors.Is(err, ErrUnavailable) {
		t.Fatal("cross-domain ciphertext accepted")
	}
	db.must(t, rqlite.Statement{SQL: `DELETE FROM setting_members WHERE setting_key='olcrtc' AND member_key=?`, Args: []any{doc.Members[0].MemberHMAC}})
	if _, err := service.ReadLegacyRuntimeSetting(ctx, "olcrtc"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing stored member silently omitted")
	}
}

func TestLegacyRuntimeSettingRejectsAuthenticatedTargetSubstitution(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	seedExactLoginCustomer(t, db, service, "Alice")
	identity, err := service.ResolveCustomerLogin(context.Background(), "Alice")
	if err != nil {
		t.Fatal(err)
	}
	member := LegacyRuntimeMember{Login: identity.Login(), CustomerSourceKey: identity.source, CustomerID: strings.Repeat("f", 64), CustomerSHA256: identity.digest, LoginHMAC: identity.LookupHMAC(), MemberHMAC: identity.SettingMemberHMAC("olcrtc")}
	doc := LegacyRuntimeSettingDocument{SchemaVersion: 1, SettingKey: "olcrtc", CapsuleSHA256: strings.Repeat("a", 64), CustomersSHA256: strings.Repeat("b", 64), ConfigJSON: json.RawMessage(`{"logins":["Alice"]}`), Members: []LegacyRuntimeMember{member}}
	encoded, digest := runtimeDomainCipher(t, service.store.secrets, "olcrtc", doc)
	if _, err := AuthenticateLegacyRuntimeSetting(service.store.secrets, "olcrtc", encoded, digest); !errors.Is(err, ErrUnavailable) {
		t.Fatal("authenticated document remapped a customer")
	}
}

func TestLegacyRuntimeSettingConventionalAbsenceIsDistinctFromBrokenNative(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	db.must(t, rqlite.Statement{SQL: `INSERT INTO cluster_settings(setting_key,public_value_json,generation,updated_at_unix) VALUES('olcrtc','{}',1,1)`})
	if _, err := service.ReadLegacyRuntimeSetting(ctx, "olcrtc"); !errors.Is(err, ErrNotFound) {
		t.Fatal("conventional setting was treated as broken native source")
	}
	doc := LegacyRuntimeSettingDocument{SchemaVersion: 1, SettingKey: "olcrtc", CapsuleSHA256: strings.Repeat("a", 64), CustomersSHA256: strings.Repeat("b", 64), ConfigJSON: json.RawMessage(`{"logins":[]}`), Members: []LegacyRuntimeMember{}}
	encoded, digest := runtimeDomainCipher(t, service.store.secrets, "olcrtc", doc)
	seedRuntimeDomainMarker(t, db, "olcrtc", doc.CapsuleSHA256, digest, encoded)
	if _, err := service.ReadLegacyRuntimeSetting(ctx, "olcrtc"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing native target secret allowed legacy fallback")
	}
}
