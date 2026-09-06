package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
	"golang.org/x/crypto/bcrypt"
)

var ErrLegacyRuntimeDomains = errors.New("legacy runtime domain source is incomplete or inconsistent")

type LegacyRuntimeProcess = controlplane.LegacyRuntimeProcess

type LegacyRuntimeFile struct {
	Path      string `json:"path"`
	State     string `json:"state"`
	RawBase64 string `json:"raw_base64"`
	SHA256    string `json:"sha256"`
}

type LegacyRuntimeCapsule struct {
	SchemaVersion int                          `json:"schema_version"`
	CapturedAt    time.Time                    `json:"captured_at"`
	CompletedAt   time.Time                    `json:"completed_at"`
	ProcessBefore LegacyRuntimeProcess         `json:"process_before"`
	ProcessAfter  LegacyRuntimeProcess         `json:"process_after"`
	CustomerCount int                          `json:"customer_count"`
	Environment   map[string]string            `json:"environment"`
	Files         map[string]LegacyRuntimeFile `json:"files"`
}

type LegacyRuntimeDomainOptions struct {
	Now             time.Time
	MaxCaptureAge   time.Duration
	ExpectedProcess LegacyRuntimeProcess
}

type LegacyRuntimeSource struct {
	RawCapsule    []byte
	RawOTAAbsence []byte
}

type LegacyRuntimeOTAAbsence = controlplane.LegacyRuntimeOTAAbsence

// The result is bound to one authenticated customer snapshot. It cannot be
// populated by deserializing an arbitrary producer response.
type LegacyRuntimeDomains struct {
	baseDigest, capsuleSHA, otaSHA string
	settings                       []LegacySetting
	principals                     []LegacyPrincipal
	secrets                        []LegacyEncryptedSecret
}

func runtimeDomainDecode(raw []byte, into any) error {
	if len(raw) == 0 || len(raw) > 8<<20 || rejectDuplicateLegacyJSON(raw) != nil || rejectLegacySurrogates(raw) != nil {
		return ErrLegacyRuntimeDomains
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ErrLegacyRuntimeDomains
	}
	return nil
}

func runtimeSourceFiles(capsule LegacyRuntimeCapsule) (map[string][]byte, error) {
	keys := []string{"MAESTRO_OLC_FILE", "MAESTRO_OLC_LOGINS", "MAESTRO_VKTURN_FILE", "MAESTRO_PANEL_PATH", "MAESTRO_PANEL_PASSWORD_HASH", "MAESTRO_PANEL_PW_FILE", "MAESTRO_OLC_WB_TOKEN_FILE"}
	if len(capsule.Environment) != len(keys) || len(capsule.Files) != 4 {
		return nil, ErrLegacyRuntimeDomains
	}
	for _, key := range keys {
		value, exists := capsule.Environment[key]
		if !exists || len(value) > 65536 || strings.ContainsRune(value, 0) {
			return nil, ErrLegacyRuntimeDomains
		}
	}
	selected := func(key, fallback string) string {
		if value := capsule.Environment[key]; value != "" {
			return value
		}
		return fallback
	}
	paths := map[string]string{
		"olcrtc":         selected("MAESTRO_OLC_FILE", "/var/lib/maestro/olcrtc.json"),
		"vkturn":         strings.TrimSpace(capsule.Environment["MAESTRO_VKTURN_FILE"]),
		"panel_password": selected("MAESTRO_PANEL_PW_FILE", "/var/lib/maestro/panel-pw.hash"),
		"wb_token":       selected("MAESTRO_OLC_WB_TOKEN_FILE", "/var/lib/maestro/wb.token"),
	}
	files := map[string][]byte{}
	for key, expected := range paths {
		file, exists := capsule.Files[key]
		raw, decodeErr := base64.StdEncoding.Strict().DecodeString(file.RawBase64)
		if !exists || file.State != "present" || !path.IsAbs(expected) || path.Clean(expected) != expected || file.Path != expected ||
			decodeErr != nil || base64.StdEncoding.EncodeToString(raw) != file.RawBase64 || len(raw) > 1<<20 || !validCanonicalSHA256(file.SHA256) || sha256Hex(raw) != file.SHA256 {
			return nil, ErrLegacyRuntimeDomains
		}
		files[key] = raw
	}
	return files, nil
}

// NormalizeLegacyRuntimeDomains preserves the actual selected source bytes and
// derives only their existing runtime meaning. It never changes transport keys,
// passwords, customer IDs or membership casing.
func NormalizeLegacyRuntimeDomains(raw, customersRaw []byte, snapshot Snapshot, box *controlplane.SecretBox, options LegacyRuntimeDomainOptions) (*LegacyRuntimeDomains, error) {
	failed := func() (*LegacyRuntimeDomains, error) { return nil, ErrLegacyRuntimeDomains }
	var capsule LegacyRuntimeCapsule
	if runtimeDomainDecode(raw, &capsule) != nil || box == nil || capsule.SchemaVersion != 1 || snapshot.SnapshotKind != "full" ||
		capsule.ProcessBefore != capsule.ProcessAfter || capsule.ProcessBefore != options.ExpectedProcess ||
		capsule.ProcessBefore.PID <= 0 || capsule.ProcessBefore.StartTicks == 0 ||
		!validCanonicalSHA256(capsule.ProcessBefore.ExecutableSHA256) || !validCanonicalSHA256(capsule.ProcessBefore.EnvironmentSHA256) ||
		capsule.ProcessBefore.CustomersSHA256 != sha256Hex(customersRaw) || snapshot.SourceHashes["customers"] != sha256Hex(customersRaw) ||
		options.Now.IsZero() || options.MaxCaptureAge <= 0 || capsule.CapturedAt.Unix() <= 0 ||
		capsule.CompletedAt.Before(capsule.CapturedAt) || capsule.CompletedAt.After(options.Now) || options.Now.Sub(capsule.CapturedAt) > options.MaxCaptureAge {
		return failed()
	}
	customers, err := DecodeLegacyCustomers(customersRaw)
	if err != nil || len(customers) != capsule.CustomerCount || len(snapshot.Customers) != len(customers) {
		return failed()
	}
	if _, err := validateProductionCustomerRows(ProtectionFromSnapshot(snapshot), box); err != nil {
		return failed()
	}
	secrets := map[string]LegacyEncryptedSecret{}
	for _, secret := range snapshot.EncryptedSecrets {
		secrets[secret.SecretID] = secret
	}
	rows := map[string]LegacyCustomer{}
	for _, row := range snapshot.Customers {
		rows[row.Login] = row
	}
	for _, customer := range customers {
		row, exists := rows[customer.Login]
		identity, err := openProductionIdentity(box, row.SourceKey, secrets[row.IdentitySecretRef])
		if !exists || err != nil || canonicalLegacyDigest(identity.Customer) != canonicalLegacyDigest(customer) {
			return failed()
		}
	}
	return deriveLegacyRuntimeDomains(raw, capsule, snapshot, box, true)
}

// The same source-to-runtime mapping is used by the producer and the import
// authentication boundary. Validation computes plaintext digests without
// resealing or changing durable source ciphertext.
func deriveLegacyRuntimeDomains(raw []byte, capsule LegacyRuntimeCapsule, snapshot Snapshot, box *controlplane.SecretBox, encrypt bool) (*LegacyRuntimeDomains, error) {
	failed := func() (*LegacyRuntimeDomains, error) { return nil, ErrLegacyRuntimeDomains }
	rows := map[string]LegacyCustomer{}
	for _, row := range snapshot.Customers {
		rows[row.Login] = row
	}
	files, err := runtimeSourceFiles(capsule)
	if err != nil {
		return failed()
	}
	defer func() {
		for _, raw := range files {
			zeroBytes(raw)
		}
	}()
	var olc olcconf.Config
	var vk vkturnconf.Config
	if runtimeDomainDecode(files["olcrtc"], &olc) != nil || runtimeDomainDecode(files["vkturn"], &vk) != nil || vk.Validate() != nil {
		return failed()
	}
	if len(olc.Logins) == 0 {
		seed := capsule.Environment["MAESTRO_OLC_LOGINS"]
		if strings.TrimSpace(seed) == "" {
			seed = "wapmix"
		}
		for _, login := range strings.Split(seed, ",") {
			if login = strings.TrimSpace(login); login != "" {
				olc.Logins = append(olc.Logins, login)
			}
		}
	}
	result := &LegacyRuntimeDomains{baseDigest: digestSnapshot(snapshot), capsuleSHA: sha256Hex(raw)}
	seal := func(id string, scope controlplane.SecretScope, plain []byte) error {
		if !encrypt {
			result.secrets = append(result.secrets, LegacyEncryptedSecret{SecretID: id, OwnerType: scope.OwnerType, OwnerSourceKey: scope.OwnerID, Field: scope.Field, Kind: scope.Kind, SHA256: sha256Hex(plain)})
			return nil
		}
		envelope, err := box.Seal(scope, plain)
		if err != nil {
			return ErrLegacyRuntimeDomains
		}
		result.secrets = append(result.secrets, LegacyEncryptedSecret{SecretID: id, OwnerType: scope.OwnerType, OwnerSourceKey: scope.OwnerID, Field: scope.Field, Kind: scope.Kind, KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce), CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: sha256Hex(plain)})
		return nil
	}
	members := func(key string, logins []string) ([]controlplane.LegacyRuntimeMember, error) {
		logins = append([]string(nil), logins...)
		sort.Strings(logins)
		values := make([]controlplane.LegacyRuntimeMember, 0, len(logins))
		for index, login := range logins {
			row, exists := rows[login]
			if !exists || (index > 0 && logins[index-1] == login) || !strings.HasPrefix(row.SourceKey, controlplane.LegacyExactCustomerSourcePrefix) {
				return nil, ErrLegacyRuntimeDomains
			}
			values = append(values, controlplane.LegacyRuntimeMember{Login: login, CustomerSourceKey: row.SourceKey,
				CustomerID: deterministicID("maestro-legacy-v1", "customer", row.SourceKey), CustomerSHA256: canonicalLegacyDigest(row),
				LoginHMAC: row.LoginKeyHMAC, MemberHMAC: box.LookupHMAC("setting-member:"+key, []byte(login))})
		}
		return values, nil
	}
	olcMembers, err := members("olcrtc", olc.Logins)
	if err != nil {
		return failed()
	}
	publicRooms := map[string]map[string]string{}
	for _, member := range olcMembers {
		room, _, ready := olc.RoomFor(member.Login)
		if !ready {
			return failed()
		}
		publicRooms[member.Login] = map[string]string{"room": room, "provider": olc.ProviderFor(member.Login)}
	}
	for login := range olc.Rooms {
		if _, exists := publicRooms[login]; !exists {
			return failed()
		}
	}
	vkLogins := make([]string, 0, len(vk.Clients))
	for login := range vk.Clients {
		vkLogins = append(vkLogins, login)
	}
	vkMembers, err := members("vkturn", vkLogins)
	if err != nil {
		return failed()
	}
	for _, item := range []struct {
		key     string
		config  any
		public  any
		members []controlplane.LegacyRuntimeMember
	}{
		{"olcrtc", olc, map[string]any{"enabled": olc.Enabled, "provider": olc.Provider, "transport": olc.Transport, "rooms": publicRooms}, olcMembers},
		{"vkturn", vk, map[string]any{"enabled": vk.Enabled, "min_version_code": vk.MinVersionCode}, vkMembers},
	} {
		config, err := json.Marshal(item.config)
		if err != nil {
			return failed()
		}
		document := controlplane.LegacyRuntimeSettingDocument{SchemaVersion: 1, SettingKey: item.key, CapsuleSHA256: result.capsuleSHA, CustomersSHA256: capsule.ProcessBefore.CustomersSHA256, ConfigJSON: config, Members: item.members}
		plain, err := json.Marshal(document)
		if err != nil {
			return failed()
		}
		id := "runtime-setting-v1:" + item.key + ":" + result.capsuleSHA
		if seal(id, controlplane.SecretScope{OwnerType: "setting", OwnerID: item.key, Field: "secret", Kind: item.key}, plain) != nil {
			zeroBytes(plain)
			return failed()
		}
		zeroBytes(plain)
		public, err := json.Marshal(item.public)
		if err != nil {
			return failed()
		}
		result.settings = append(result.settings, LegacySetting{Key: item.key, PublicValueJSON: public, Generation: 1, SecretRef: id, Members: append([]controlplane.LegacyRuntimeMember(nil), item.members...)})
	}
	verifier := capsule.Environment["MAESTRO_PANEL_PASSWORD_HASH"]
	if fromFile := strings.TrimSpace(string(files["panel_password"])); fromFile != "" {
		verifier = fromFile
	}
	if capsule.Environment["MAESTRO_PANEL_PATH"] == "" || capsule.Environment["MAESTRO_PANEL_PASSWORD_HASH"] == "" {
		return failed()
	}
	if _, err := bcrypt.Cost([]byte(verifier)); err != nil {
		return failed()
	}
	principalSource, principalSecret := "s1:panel-owner-v1", "runtime-panel-password-v1:"+result.capsuleSHA
	if seal(principalSecret, controlplane.SecretScope{OwnerType: "principal", OwnerID: principalSource, Field: "password", Kind: "bcrypt"}, []byte(verifier)) != nil {
		return failed()
	}
	result.principals = []LegacyPrincipal{{SourceKey: principalSource, LoginKeyHMAC: box.LookupHMAC("principal-login", []byte("legacy-panel-owner")), Status: "active", Roles: []string{"owner"}, CredentialSecretRef: principalSecret}}
	// WB's existing secret consumer expects the trimmed token itself, not a new wrapper.
	word := strings.TrimSpace(string(files["wb_token"]))
	if word == "" || len(word) > 65536 {
		return failed()
	}
	wbID := "runtime-setting-v1:wbstream:" + result.capsuleSHA
	if seal(wbID, controlplane.SecretScope{OwnerType: "setting", OwnerID: "wbstream", Field: "secret", Kind: "wbstream"}, []byte(word)) != nil {
		return failed()
	}
	result.settings = append(result.settings, LegacySetting{Key: "wbstream", PublicValueJSON: json.RawMessage(`{}`), Generation: 1, SecretRef: wbID})
	if seal("legacy-runtime-source-v1:"+result.capsuleSHA, controlplane.SecretScope{OwnerType: "legacy_runtime_source", OwnerID: result.capsuleSHA, Field: "raw_capsule", Kind: "runtime-domains-v1"}, raw) != nil {
		return failed()
	}
	return result, nil
}

func ComposeLegacyRuntimeDomains(snapshot Snapshot, domains *LegacyRuntimeDomains) (Snapshot, error) {
	if domains == nil || domains.baseDigest == "" || digestSnapshot(snapshot) != domains.baseDigest {
		return Snapshot{}, ErrLegacyRuntimeDomains
	}
	for _, domain := range []string{"settings", "principals"} {
		if source, present := snapshot.SourceHashes["legacy:"+domain+":present-unconverted"]; present && source != domains.capsuleSHA {
			return Snapshot{}, ErrLegacyRuntimeDomains
		}
	}
	for _, existing := range snapshot.Settings {
		for _, added := range domains.settings {
			if existing.Key == added.Key {
				return Snapshot{}, ErrLegacyRuntimeDomains
			}
		}
	}
	for _, existing := range snapshot.Principals {
		for _, added := range domains.principals {
			if existing.SourceKey == added.SourceKey || existing.LoginKeyHMAC == added.LoginKeyHMAC {
				return Snapshot{}, ErrLegacyRuntimeDomains
			}
		}
	}
	snapshot.Settings = append(cloneRuntimeSettings(snapshot.Settings), cloneRuntimeSettings(domains.settings)...)
	snapshot.Principals = append(clonePrincipals(snapshot.Principals), clonePrincipals(domains.principals)...)
	snapshot.EncryptedSecrets = append(append([]LegacyEncryptedSecret(nil), snapshot.EncryptedSecrets...), domains.secrets...)
	hashes := make(map[string]string, len(snapshot.SourceHashes)+2)
	for key, value := range snapshot.SourceHashes {
		hashes[key] = value
	}
	for _, domain := range []string{"settings", "principals"} {
		delete(hashes, "legacy:"+domain+":absent")
		delete(hashes, "legacy:"+domain+":present-unconverted")
		hashes["legacy:"+domain+":runtime-converted-v1"] = domains.capsuleSHA
	}
	if domains.otaSHA != "" {
		hashes["legacy:ota:absent-v1"] = domains.otaSHA
	}
	snapshot.SourceHashes = hashes
	return snapshot, nil
}

func cloneRuntimeSettings(values []LegacySetting) []LegacySetting {
	result := cloneSettings(values)
	for index := range result {
		result[index].Members = append([]controlplane.LegacyRuntimeMember(nil), values[index].Members...)
	}
	return result
}

func reservedRuntimeSecret(secret LegacyEncryptedSecret) bool {
	return reservedRuntimeSecretID(secret.SecretID) || secret.OwnerType == "legacy_runtime_source" || secret.OwnerType == "legacy_runtime_ota_source"
}

func reservedRuntimeSecretID(id string) bool {
	return strings.HasPrefix(id, "legacy-runtime-source-v1:") || strings.HasPrefix(id, "legacy-runtime-ota-source-v1:") || strings.HasPrefix(id, "runtime-setting-v1:") || strings.HasPrefix(id, "runtime-panel-password-v1:")
}

// An encrypted source capsule authenticates every derived native row. A marker
// or a typed member list alone cannot enable the production adapter.
func validateNativeRuntimeProof(protection SnapshotProtection, box *controlplane.SecretBox) (map[string]string, error) {
	if protection.SnapshotKind == "delta" && (hasNativeRuntimeSource(protection.SourceHashes) || (protection.Parent != nil && hasNativeRuntimeSource(protection.Parent.SourceHashes))) {
		return validateNativeRuntimeDeltaProof(protection, box)
	}
	if _, exists := protection.SourceHashes[legacyRuntimeCurrentCapture]; exists {
		return nil, ErrLegacyRuntimeDomains
	}
	if _, exists := protection.SourceHashes[legacyRuntimeCurrentOTA]; exists {
		return nil, ErrLegacyRuntimeDomains
	}
	sha, present := protection.SourceHashes["legacy:settings:runtime-converted-v1"]
	principalSHA, principalPresent := protection.SourceHashes["legacy:principals:runtime-converted-v1"]
	reserved := map[string]LegacyEncryptedSecret{}
	for _, secret := range protection.EncryptedSecrets {
		if reservedRuntimeSecret(secret) {
			if _, exists := reserved[secret.SecretID]; exists {
				return nil, ErrLegacyRuntimeDomains
			}
			reserved[secret.SecretID] = secret
		}
	}
	if !present && !principalPresent {
		if len(reserved) != 0 {
			return nil, ErrLegacyRuntimeDomains
		}
		for _, setting := range protection.Settings {
			if len(setting.Members) != 0 {
				return nil, ErrLegacyRuntimeDomains
			}
		}
		return nil, nil
	}
	if box == nil || !present || !principalPresent || !validCanonicalSHA256(sha) || sha != principalSHA || protection.SnapshotKind != "full" {
		return nil, ErrLegacyRuntimeDomains
	}
	for _, domain := range []string{"settings", "principals"} {
		for _, state := range []string{"absent", "present-unconverted"} {
			if _, exists := protection.SourceHashes["legacy:"+domain+":"+state]; exists {
				return nil, ErrLegacyRuntimeDomains
			}
		}
	}
	source, exists := reserved["legacy-runtime-source-v1:"+sha]
	if !exists || source.OwnerType != "legacy_runtime_source" || source.OwnerSourceKey != sha || source.Field != "raw_capsule" || source.Kind != "runtime-domains-v1" || source.SHA256 != sha {
		return nil, ErrLegacyRuntimeDomains
	}
	open := func(secret LegacyEncryptedSecret) ([]byte, error) {
		nonce, nOK := decodeCanonicalBase64(secret.NonceB64)
		cipher, cOK := decodeCanonicalBase64(secret.CiphertextB64)
		if !nOK || !cOK || secret.KeyVersion <= 0 {
			return nil, ErrLegacyRuntimeDomains
		}
		plain, err := box.Open(controlplane.SecretScope{OwnerType: secret.OwnerType, OwnerID: secret.OwnerSourceKey, Field: secret.Field, Kind: secret.Kind}, controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: cipher})
		if err != nil || sha256Hex(plain) != secret.SHA256 {
			zeroBytes(plain)
			return nil, ErrLegacyRuntimeDomains
		}
		return plain, nil
	}
	raw, err := open(source)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(raw)
	var capsule LegacyRuntimeCapsule
	if runtimeDomainDecode(raw, &capsule) != nil || capsule.SchemaVersion != 1 || capsule.ProcessBefore != capsule.ProcessAfter || capsule.ProcessBefore.PID <= 0 || capsule.ProcessBefore.StartTicks == 0 || !validCanonicalSHA256(capsule.ProcessBefore.ExecutableSHA256) || !validCanonicalSHA256(capsule.ProcessBefore.EnvironmentSHA256) || capsule.ProcessBefore.CustomersSHA256 != protection.SourceHashes["customers"] || capsule.CustomerCount != len(protection.Customers) || capsule.CapturedAt.Unix() <= 0 || capsule.CompletedAt.Before(capsule.CapturedAt) {
		return nil, ErrLegacyRuntimeDomains
	}
	allSecrets := map[string]LegacyEncryptedSecret{}
	for _, secret := range protection.EncryptedSecrets {
		allSecrets[secret.SecretID] = secret
	}
	for _, customer := range protection.Customers {
		identity, err := openProductionIdentity(box, customer.SourceKey, allSecrets[customer.IdentitySecretRef])
		if err != nil || validateProductionIdentity(box, customer, identity) != nil {
			return nil, ErrLegacyRuntimeDomains
		}
	}
	expected, err := deriveLegacyRuntimeDomains(raw, capsule, Snapshot{Customers: protection.Customers, SourceHashes: protection.SourceHashes}, box, false)
	if err != nil {
		return nil, ErrLegacyRuntimeDomains
	}
	if otaSHA, present := protection.SourceHashes["legacy:ota:absent-v1"]; present {
		if !validCanonicalSHA256(otaSHA) {
			return nil, ErrLegacyRuntimeDomains
		}
		ota, exists := reserved["runtime-setting-v1:ota:"+otaSHA]
		if !exists {
			return nil, ErrLegacyRuntimeDomains
		}
		otaRaw, err := open(ota)
		if err != nil {
			return nil, err
		}
		err = expected.appendOTAAbsence(otaRaw, capsule, box, false, time.Time{}, 0)
		zeroBytes(otaRaw)
		if err != nil {
			return nil, err
		}
	}
	if len(expected.secrets) != len(reserved) {
		return nil, ErrLegacyRuntimeDomains
	}
	settings := map[string]LegacySetting{}
	for _, row := range protection.Settings {
		if _, duplicate := settings[row.Key]; duplicate {
			return nil, ErrLegacyRuntimeDomains
		}
		settings[row.Key] = row
	}
	for _, row := range expected.settings {
		if canonicalLegacyDigest(settings[row.Key]) != canonicalLegacyDigest(row) {
			return nil, ErrLegacyRuntimeDomains
		}
		delete(settings, row.Key)
	}
	for _, row := range settings {
		if len(row.Members) != 0 || reservedRuntimeSecretID(row.SecretRef) {
			return nil, ErrLegacyRuntimeDomains
		}
	}
	principals := map[string]LegacyPrincipal{}
	for _, row := range protection.Principals {
		if _, duplicate := principals[row.SourceKey]; duplicate {
			return nil, ErrLegacyRuntimeDomains
		}
		principals[row.SourceKey] = row
	}
	for _, row := range expected.principals {
		if canonicalLegacyDigest(principals[row.SourceKey]) != canonicalLegacyDigest(row) {
			return nil, ErrLegacyRuntimeDomains
		}
		delete(principals, row.SourceKey)
	}
	for _, row := range principals {
		if reservedRuntimeSecretID(row.CredentialSecretRef) {
			return nil, ErrLegacyRuntimeDomains
		}
	}
	proof := map[string]string{}
	for _, want := range expected.secrets {
		actual, exists := reserved[want.SecretID]
		if !exists || actual.OwnerType != want.OwnerType || actual.OwnerSourceKey != want.OwnerSourceKey || actual.Field != want.Field || actual.Kind != want.Kind || actual.SHA256 != want.SHA256 {
			return nil, ErrLegacyRuntimeDomains
		}
		plain, err := open(actual)
		zeroBytes(plain)
		if err != nil {
			return nil, err
		}
		proof[actual.SecretID] = canonicalLegacyDigest(actual)
	}
	return proof, nil
}

func normalizeLegacyRuntimeSource(snapshot *Snapshot, rawCustomers []byte, source *LegacyRuntimeSource, box *controlplane.SecretBox, now time.Time, maxAge time.Duration, parent *Snapshot) error {
	if source == nil {
		if parent != nil {
			if _, exists := parent.SourceHashes["legacy:settings:runtime-converted-v1"]; exists {
				return ErrLegacyRuntimeDomains
			}
		}
		return nil
	}
	// Runtime capture is a protected root-owned input. The capture producer binds
	// its before/after process tuple; the importer independently binds its exact
	// customer bytes and freshness. It never makes a new live observation.
	var capsule LegacyRuntimeCapsule
	if runtimeDomainDecode(source.RawCapsule, &capsule) != nil {
		return ErrLegacyRuntimeDomains
	}
	if parent != nil {
		return normalizeLegacyRuntimeDelta(snapshot, rawCustomers, source, box, now, maxAge, parent)
	}
	for _, domain := range []string{"settings", "principals"} {
		if sourceSHA, present := snapshot.SourceHashes["legacy:"+domain+":present-unconverted"]; present && sourceSHA != sha256Hex(source.RawCapsule) {
			return ErrLegacyRuntimeDomains
		}
	}
	value, err := NormalizeLegacyRuntimeDomains(source.RawCapsule, rawCustomers, *snapshot, box, LegacyRuntimeDomainOptions{Now: now, MaxCaptureAge: maxAge, ExpectedProcess: capsule.ProcessBefore})
	if err != nil {
		return err
	}
	if len(source.RawOTAAbsence) > 0 {
		if err := value.appendOTAAbsence(source.RawOTAAbsence, capsule, box, true, now, maxAge); err != nil {
			return err
		}
	}
	composed, err := ComposeLegacyRuntimeDomains(*snapshot, value)
	if err != nil {
		return err
	}
	*snapshot = composed
	return nil
}

func (domains *LegacyRuntimeDomains) appendOTAAbsence(raw []byte, capsule LegacyRuntimeCapsule, box *controlplane.SecretBox, encrypt bool, reference time.Time, maxAge time.Duration) error {
	var evidence LegacyRuntimeOTAAbsence
	if runtimeDomainDecode(raw, &evidence) != nil || evidence.SchemaVersion != 1 || evidence.State != "absent" || evidence.Process != capsule.ProcessBefore || evidence.RuntimeCapsuleSHA256 != domains.capsuleSHA || !path.IsAbs(evidence.Directory) || path.Clean(evidence.Directory) != evidence.Directory || strings.ContainsRune(evidence.Directory, 0) || evidence.ObservedAt.Unix() <= 0 || (encrypt && (reference.IsZero() || maxAge <= 0 || evidence.ObservedAt.Before(reference.Add(-maxAge)) || evidence.ObservedAt.After(reference))) {
		return ErrLegacyRuntimeDomains
	}
	domains.otaSHA = sha256Hex(raw)
	id := "runtime-setting-v1:ota:" + domains.otaSHA
	secret := LegacyEncryptedSecret{SecretID: id, OwnerType: "setting", OwnerSourceKey: "ota", Field: "secret", Kind: "ota", SHA256: domains.otaSHA}
	if encrypt {
		envelope, err := box.Seal(controlplane.SecretScope{OwnerType: "setting", OwnerID: "ota", Field: "secret", Kind: "ota"}, raw)
		if err != nil {
			return ErrLegacyRuntimeDomains
		}
		secret.KeyVersion, secret.NonceB64, secret.CiphertextB64 = envelope.KeyVersion, base64.StdEncoding.EncodeToString(envelope.Nonce), base64.StdEncoding.EncodeToString(envelope.Ciphertext)
	}
	public, _ := json.Marshal(struct {
		State        string `json:"state"`
		SourceSHA256 string `json:"source_sha256"`
	}{"absent", domains.otaSHA})
	domains.settings = append(domains.settings, LegacySetting{Key: "ota", Generation: 1, PublicValueJSON: public, SecretRef: id})
	domains.secrets = append(domains.secrets, secret)
	return nil
}

// The existing settingStatements caller appends these after its setting and
// secret writes, in that same batch transaction. No side effect occurs here.
func (s *RQLiteApplyStore) productionRuntimeMemberStatements(batch ApplyBatch, setting LegacySetting, secret *LegacyEncryptedSecret) ([]rqlite.Statement, error) {
	native := secret != nil && strings.HasPrefix(secret.SecretID, "runtime-setting-v1:"+setting.Key+":")
	if setting.Key != "olcrtc" && setting.Key != "vkturn" {
		if len(setting.Members) != 0 {
			return nil, ErrLegacyRuntimeDomains
		}
		return nil, nil
	}
	if !native {
		if len(setting.Members) != 0 {
			return nil, ErrLegacyRuntimeDomains
		}
		return nil, nil
	}
	if s.customerProtection == nil || s.customerProtection.runtimeSecrets[secret.SecretID] != canonicalLegacyDigest(*secret) {
		return nil, ErrLegacyRuntimeDomains
	}
	value, err := s.productionSettingValue(setting, secret)
	if err != nil || value == nil {
		return nil, ErrLegacyRuntimeDomains
	}
	domain, err := controlplane.AuthenticateLegacyRuntimeSetting(s.customerProtection.box, setting.Key, value.envelope, value.digest)
	if err != nil || canonicalLegacyDigest(setting.Members) != canonicalLegacyDigest(domain.Members()) {
		return nil, ErrLegacyRuntimeDomains
	}
	guard, args := domain.MembershipGuardSQL()
	// json() on invalid input deliberately aborts the transaction on proof drift.
	statements := []rqlite.Statement{{SQL: `SELECT CASE WHEN NOT (` + batchWriteGate + `) OR (` + guard + `) THEN 1 ELSE json('invalid-runtime-member-binding') END`, Args: append(batchGateArgs(batch), args...)},
		{SQL: `DELETE FROM setting_members WHERE setting_key=? AND ` + batchWriteGate, Args: append([]any{setting.Key}, batchGateArgs(batch)...)}}
	for _, member := range domain.Members() {
		statements = append(statements, rqlite.Statement{SQL: `INSERT INTO setting_members(setting_key,member_key,member_value_json,generation) SELECT ?,?,?,? WHERE ` + batchWriteGate,
			Args: append([]any{setting.Key, member.MemberHMAC, `{"enabled":true}`, setting.Generation}, batchGateArgs(batch)...)})
	}
	return statements, nil
}
