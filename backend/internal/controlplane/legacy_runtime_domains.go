package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"path"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type LegacyRuntimeProcess struct {
	PID               int    `json:"pid"`
	StartTicks        uint64 `json:"start_ticks"`
	ExecutableSHA256  string `json:"executable_sha256"`
	EnvironmentSHA256 string `json:"environment_sha256"`
	CustomersSHA256   string `json:"customers_sha256"`
}

type LegacyRuntimeOTAAbsence struct {
	SchemaVersion        int                  `json:"schema_version"`
	ObservedAt           time.Time            `json:"observed_at"`
	Process              LegacyRuntimeProcess `json:"process"`
	Directory            string               `json:"directory"`
	State                string               `json:"state"`
	RuntimeCapsuleSHA256 string               `json:"runtime_capsule_sha256"`
}

// These documents live inside the existing setting/<key>/secret/<key> AEAD
// scope. Member names and transport credentials are never public projections.
type LegacyRuntimeMember struct {
	Login             string `json:"login"`
	CustomerSourceKey string `json:"customer_source_key"`
	CustomerID        string `json:"customer_id"`
	CustomerSHA256    string `json:"customer_sha256"`
	LoginHMAC         string `json:"login_hmac"`
	MemberHMAC        string `json:"member_hmac"`
}

type LegacyRuntimeSettingDocument struct {
	SchemaVersion   int                   `json:"schema_version"`
	SettingKey      string                `json:"setting_key"`
	CapsuleSHA256   string                `json:"capsule_sha256"`
	CustomersSHA256 string                `json:"customers_sha256"`
	ConfigJSON      json.RawMessage       `json:"config_json"`
	Members         []LegacyRuntimeMember `json:"members"`
}

type LegacyRuntimeSetting struct {
	document   LegacyRuntimeSettingDocument
	digest     string
	generation int64
	source     LegacyRuntimeSettingDocument
	sourceSHA  string
}

func (value LegacyRuntimeSetting) Generation() int64 { return value.generation }

func (value LegacyRuntimeSetting) ConfigJSON() json.RawMessage {
	return append(json.RawMessage(nil), value.document.ConfigJSON...)
}

func (value LegacyRuntimeSetting) Members() []LegacyRuntimeMember {
	return append([]LegacyRuntimeMember(nil), value.document.Members...)
}

func decodeRuntimeDomain(raw []byte, into any) error {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return ErrUnavailable
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	var visit func() error
	visit = func() error {
		token, err := tokens.Token()
		if err != nil {
			return ErrUnavailable
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for tokens.More() {
				key, err := tokens.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return ErrUnavailable
				}
				seen[name] = true
				if visit() != nil {
					return ErrUnavailable
				}
			}
			end, err := tokens.Token()
			if err != nil || end != json.Delim('}') {
				return ErrUnavailable
			}
		case json.Delim('['):
			for tokens.More() {
				if visit() != nil {
					return ErrUnavailable
				}
			}
			end, err := tokens.Token()
			if err != nil || end != json.Delim(']') {
				return ErrUnavailable
			}
		}
		return nil
	}
	if visit() != nil {
		return ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(into) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ErrUnavailable
	}
	return nil
}

func validateLegacyRuntimeSettingPlain(box *SecretBox, key string, plain []byte) (LegacyRuntimeSetting, error) {
	var doc LegacyRuntimeSettingDocument
	if box == nil || (key != "olcrtc" && key != "vkturn") || decodeRuntimeDomain(plain, &doc) != nil ||
		doc.SchemaVersion != 1 || doc.SettingKey != key || !customerIdentityDigest(doc.CapsuleSHA256) ||
		!customerIdentityDigest(doc.CustomersSHA256) || !json.Valid(doc.ConfigJSON) || doc.Members == nil || len(doc.Members) > 10000 {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	seen := map[string]bool{}
	logins := map[string]bool{}
	for _, member := range doc.Members {
		canonical, err := CanonicalLoginKey(member.Login)
		family, exact, valid := ParseLegacyExactCustomerSource(member.CustomerSourceKey)
		id := sha256.Sum256([]byte("maestro-legacy-v1\x00customer\x00" + member.CustomerSourceKey))
		if err != nil || !valid || family != box.LookupHMAC("customer-login", []byte(canonical)) ||
			exact != box.LookupHMAC(LegacyExactCustomerLoginHMACDomain, []byte(member.Login)) ||
			member.LoginHMAC != exact || member.CustomerID != hex.EncodeToString(id[:]) ||
			!customerIdentityDigest(member.CustomerSHA256) || member.MemberHMAC != box.LookupHMAC("setting-member:"+key, []byte(member.Login)) || seen[member.CustomerID] {
			return LegacyRuntimeSetting{}, ErrUnavailable
		}
		seen[member.CustomerID] = true
		logins[member.Login] = true
	}
	var config map[string]json.RawMessage
	if decodeRuntimeDomain(doc.ConfigJSON, &config) != nil || config == nil {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	if key == "olcrtc" {
		var allowed []string
		if decodeRuntimeDomain(config["logins"], &allowed) != nil || len(allowed) != len(logins) {
			return LegacyRuntimeSetting{}, ErrUnavailable
		}
		for _, login := range allowed {
			if !logins[login] {
				return LegacyRuntimeSetting{}, ErrUnavailable
			}
			delete(logins, login)
		}
	} else {
		var clients map[string]json.RawMessage
		if decodeRuntimeDomain(config["clients"], &clients) != nil || clients == nil || len(clients) != len(logins) {
			return LegacyRuntimeSetting{}, ErrUnavailable
		}
		for login := range clients {
			if !logins[login] {
				return LegacyRuntimeSetting{}, ErrUnavailable
			}
			delete(logins, login)
		}
	}
	digest := sha256.Sum256(plain)
	return LegacyRuntimeSetting{document: doc, digest: hex.EncodeToString(digest[:])}, nil
}

func AuthenticateLegacyRuntimeSetting(box *SecretBox, key, encoded, digest string) (LegacyRuntimeSetting, error) {
	if box == nil || len(encoded) > 2<<20 || !customerIdentityDigest(digest) {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	var envelope Envelope
	if err != nil || base64.StdEncoding.EncodeToString(raw) != encoded || decodeRuntimeDomain(raw, &envelope) != nil {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	plain, err := box.Open(SecretScope{OwnerType: "setting", OwnerID: key, Field: "secret", Kind: key}, envelope)
	if err != nil {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	defer wipeDesiredPayloadBytes(plain)
	value, err := validateLegacyRuntimeSettingPlain(box, key, plain)
	if err != nil || value.digest != digest {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	return value, nil
}

// MembershipGuardSQL is available only on a successfully decoded value. The
// importer combines it with its existing batch gate in the same transaction.
func (value LegacyRuntimeSetting) MembershipGuardSQL() (string, []any) {
	if value.digest == "" {
		return "0=1", nil
	}
	guards := []string{"1=1"}
	var args []any
	for _, member := range value.document.Members {
		guards = append(guards, `EXISTS(SELECT 1 FROM customers c JOIN imported_entity_state e ON e.entity_kind='customer' AND e.target_id=c.customer_id WHERE c.customer_id=? AND c.display_login=? AND c.login_key_hmac=? AND e.source_key=? AND e.canonical_sha256=? AND e.lifecycle='active')`)
		args = append(args, member.CustomerID, member.Login, member.LoginHMAC, member.CustomerSourceKey, member.CustomerSHA256)
	}
	return strings.Join(guards, " AND "), args
}

// ReadLegacyRuntimeSetting is the authenticated reader seam for the existing
// OLC/VKTURN materializers. It returns no partial configuration when a member,
// imported identity, generation or envelope has drifted.
func (s *Service) ReadLegacyRuntimeSetting(ctx context.Context, key string) (LegacyRuntimeSetting, error) {
	if s == nil || s.store == nil || (key != "olcrtc" && key != "vkturn") {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	results, err := s.store.db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT c.generation,s.secret_envelope,s.secret_sha256 FROM cluster_settings c LEFT JOIN setting_secrets s ON s.setting_key=c.setting_key WHERE c.setting_key=?`, Args: []any{key}},
		rqlite.Statement{SQL: `SELECT member_key,member_value_json,generation FROM setting_members WHERE setting_key=? ORDER BY member_key`, Args: []any{key}},
		rqlite.Statement{SQL: `SELECT i.secret_id,i.secret_sha256,i.secret_envelope AS source_envelope,i.key_version,e.canonical_sha256 AS source_envelope_sha256,e.lifecycle FROM imported_secrets i LEFT JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=i.secret_id AND e.target_id=i.secret_id WHERE i.owner_type='setting' AND i.owner_source_key=? AND i.field='secret' AND i.kind=? AND i.secret_id LIKE ? ORDER BY i.secret_id`, Args: []any{key, key, "runtime-setting-v1:" + key + ":%"}},
	)
	if err != nil || len(results) != 3 {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	if len(results[0].Rows) == 0 && len(results[1].Rows) == 0 && len(results[2].Rows) == 0 {
		return LegacyRuntimeSetting{}, ErrNotFound
	}
	if len(results[0].Rows) != 1 {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	row := results[0].Rows[0]
	if row["secret_envelope"] == nil && row["secret_sha256"] == nil && len(results[2].Rows) == 0 {
		return LegacyRuntimeSetting{}, ErrNotFound
	}
	encoded, eOK := rowString(row, "secret_envelope")
	digest, dOK := rowString(row, "secret_sha256")
	generation, gOK := rowInt64(row, "generation")
	if !eOK || !dOK || !gOK || generation < 1 {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	if len(results[2].Rows) != 1 {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	source, err := authenticateRuntimeSource(s.store.secrets, key, results[2].Rows[0])
	if err != nil {
		return LegacyRuntimeSetting{}, err
	}
	value, err := authenticateRuntimeCurrent(s.store.secrets, key, encoded, digest, generation, source)
	if err != nil || len(results[1].Rows) != len(value.document.Members) {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	members := map[string]bool{}
	for _, row := range results[1].Rows {
		member, mOK := rowString(row, "member_key")
		body, bOK := rowString(row, "member_value_json")
		version, vOK := rowInt64(row, "generation")
		var enabled struct {
			Enabled bool `json:"enabled"`
		}
		if !mOK || !bOK || !vOK || version != generation || members[member] || decodeRuntimeDomain([]byte(body), &enabled) != nil || !enabled.Enabled {
			return LegacyRuntimeSetting{}, ErrUnavailable
		}
		members[member] = true
	}
	for _, member := range value.document.Members {
		identity, err := s.ResolveCustomerLogin(ctx, member.Login)
		if err != nil || !identity.Exists() || !identity.ExactLegacy() || identity.CustomerID() != member.CustomerID || identity.source != member.CustomerSourceKey || identity.LookupHMAC() != member.LoginHMAC || !members[identity.SettingMemberHMAC(key)] {
			return LegacyRuntimeSetting{}, ErrUnavailable
		}
	}
	value.generation = generation
	return value, nil
}

// ReadLegacyOTAAbsent distinguishes an authenticated imported absence from a
// conventional existing manifest. A public flag cannot suppress the OTA route.
func (s *Service) ReadLegacyOTAAbsent(ctx context.Context) (bool, error) {
	if s == nil || s.store == nil {
		return false, ErrUnavailable
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT c.public_value_json,s.secret_envelope,s.secret_sha256,i.secret_id,i.secret_sha256 AS source_sha256,e.lifecycle FROM cluster_settings c LEFT JOIN setting_secrets s ON s.setting_key=c.setting_key LEFT JOIN imported_secrets i ON i.owner_type='setting' AND i.owner_source_key='ota' AND i.field='secret' AND i.kind='ota' AND i.secret_id LIKE 'runtime-setting-v1:ota:%' LEFT JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=i.secret_id AND e.target_id=i.secret_id WHERE c.setting_key='ota'`})
	if err != nil || len(results) != 1 {
		return false, ErrUnavailable
	}
	if len(results[0].Rows) == 0 {
		return false, ErrNotFound
	}
	if len(results[0].Rows) != 1 {
		return false, ErrUnavailable
	}
	row := results[0].Rows[0]
	public, ok := rowString(row, "public_value_json")
	if !ok {
		return false, ErrUnavailable
	}
	var object map[string]json.RawMessage
	if decodeRuntimeDomain([]byte(public), &object) != nil || object == nil {
		return false, ErrUnavailable
	}
	if _, hasState := object["state"]; !hasState {
		if row["secret_envelope"] != nil || row["secret_sha256"] != nil || row["secret_id"] != nil {
			return false, ErrUnavailable
		}
		return false, nil
	}
	var declaration struct {
		State        string `json:"state"`
		SourceSHA256 string `json:"source_sha256"`
	}
	if decodeRuntimeDomain([]byte(public), &declaration) != nil || declaration.State != "absent" || !customerIdentityDigest(declaration.SourceSHA256) {
		return false, ErrUnavailable
	}
	encoded, eOK := rowString(row, "secret_envelope")
	digest, dOK := rowString(row, "secret_sha256")
	sourceID, iOK := rowString(row, "secret_id")
	sourceSHA, hOK := rowString(row, "source_sha256")
	lifecycle, lOK := rowString(row, "lifecycle")
	if !eOK || !dOK || !iOK || !hOK || !lOK || digest != declaration.SourceSHA256 || sourceSHA != digest || sourceID != "runtime-setting-v1:ota:"+digest || lifecycle != "active" {
		return false, ErrUnavailable
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	var envelope Envelope
	if err != nil || base64.StdEncoding.EncodeToString(raw) != encoded || decodeRuntimeDomain(raw, &envelope) != nil {
		return false, ErrUnavailable
	}
	plain, err := s.store.secrets.Open(SecretScope{OwnerType: "setting", OwnerID: "ota", Field: "secret", Kind: "ota"}, envelope)
	if err != nil {
		return false, ErrUnavailable
	}
	defer wipeDesiredPayloadBytes(plain)
	sum := sha256.Sum256(plain)
	var evidence LegacyRuntimeOTAAbsence
	if hex.EncodeToString(sum[:]) != digest || decodeRuntimeDomain(plain, &evidence) != nil || evidence.SchemaVersion != 1 || evidence.State != "absent" || evidence.ObservedAt.Unix() <= 0 || evidence.Process.PID <= 0 || evidence.Process.StartTicks == 0 || !customerIdentityDigest(evidence.Process.ExecutableSHA256) || !customerIdentityDigest(evidence.Process.EnvironmentSHA256) || !customerIdentityDigest(evidence.Process.CustomersSHA256) || !customerIdentityDigest(evidence.RuntimeCapsuleSHA256) || !path.IsAbs(evidence.Directory) || path.Clean(evidence.Directory) != evidence.Directory || strings.ContainsRune(evidence.Directory, 0) {
		return false, ErrUnavailable
	}
	return true, nil
}
