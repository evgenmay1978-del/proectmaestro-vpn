package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

var ErrLegacyNormalize = errors.New("legacy customer capture is incomplete, inconsistent, or unsupported")

// The stage is assigned only by this normalizer. It never contains source
// values or a wrapped parser/cryptographic error; the old public error remains
// unchanged for callers which do not request the diagnostic code.
type legacyNormalizeFailure struct{ stage string }

func (failure *legacyNormalizeFailure) Error() string { return ErrLegacyNormalize.Error() }
func (failure *legacyNormalizeFailure) Unwrap() error { return ErrLegacyNormalize }

func LegacyNormalizeFailureStage(err error) string {
	var failure *legacyNormalizeFailure
	if errors.As(err, &failure) && failure != nil {
		return failure.stage
	}
	return "NORMALIZER"
}

const LegacyCustomerPreparationScope = "customer-preparation-v1"

// LegacyXUICapture contains existing identities, never panel access credentials.
// CapturedAt precedes the first source read. The producer verifies the exact
// source bytes again after its final lookup, before publishing this capture.
type LegacyXUICapture struct {
	SchemaVersion   int                 `json:"schema_version"`
	CapturedAt      time.Time           `json:"captured_at"`
	CompletedAt     time.Time           `json:"completed_at"`
	CustomersSHA256 string              `json:"customers_sha256"`
	Bindings        []LegacyNodeCapture `json:"bindings"`
}

type LegacyNodeCapture struct {
	Login          string                                 `json:"login"`
	NodeID         string                                 `json:"node_id"`
	Server         string                                 `json:"server"`
	UUID           string                                 `json:"uuid"`
	SubID          string                                 `json:"sub_id"`
	ObservedAbsent *controlplane.LegacyXUIAbsenceEvidence `json:"observed_absent,omitempty"`
}

// Non-XUI protocols require an explicit source server -> production node map.
type LegacyProtocolBinding struct {
	Protocol string `json:"protocol"`
	Server   string `json:"server"`
	NodeID   string `json:"node_id"`
}

type LegacySourcePresence struct {
	State  string `json:"state"`
	SHA256 string `json:"sha256,omitempty"`
}

type LegacyNormalizeOptions struct {
	Now                  time.Time
	MaxCaptureAge        time.Duration
	Sources              map[string]LegacySourcePresence
	ProtocolBindings     []LegacyProtocolBinding
	Parent               *Snapshot
	PlanOptions          PlanOptions
	TrialSource          *LegacyTrialSource
	OrderSource          *LegacyOrderSource
	RuntimeSource        *LegacyRuntimeSource
	CompleteNativeImport bool
}

// DecodeLegacyCustomers deliberately avoids store.Open: absent sources and
// duplicate identities must not be converted to empty or overwritten records.
func DecodeLegacyCustomers(raw []byte) ([]legacystore.Customer, error) {
	if len(raw) == 0 || len(raw) > 64<<20 || !utf8.Valid(raw) || rejectLegacySurrogates(raw) != nil ||
		rejectDuplicateLegacyJSON(raw) != nil || rejectLegacyFieldAliases(raw) != nil {
		return nil, ErrLegacyNormalize
	}
	var rows []*legacystore.Customer
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&rows) != nil || rows == nil {
		return nil, ErrLegacyNormalize
	}
	logins, tokens, uuids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	customers := make([]legacystore.Customer, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			return nil, ErrLegacyNormalize
		}
		_, err := controlplane.CanonicalLoginKey(row.Login)
		if err != nil || row.SubToken == "" || row.Expires.IsZero() ||
			logins[row.Login] || tokens[row.SubToken] || (row.VLESS != nil && uuids[row.VLESS.UUID]) {
			return nil, ErrLegacyNormalize
		}
		if _, err := productionCredentials(ProductionCustomerIdentity{Customer: *row}); err != nil {
			return nil, ErrLegacyNormalize
		}
		logins[row.Login], tokens[row.SubToken] = true, true
		if row.VLESS != nil {
			uuids[row.VLESS.UUID] = true
		}
		customers = append(customers, *row)
	}
	return customers, nil
}

func rejectLegacyFieldAliases(raw []byte) error {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		return ErrLegacyNormalize
	}
	check := func(object map[string]json.RawMessage) error {
		if len(object) > 64 {
			return ErrLegacyNormalize
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			for _, prior := range keys {
				if strings.EqualFold(key, prior) {
					return ErrLegacyNormalize
				}
			}
			keys = append(keys, key)
		}
		return nil
	}
	for _, row := range rows {
		if check(row) != nil {
			return ErrLegacyNormalize
		}
		for key, value := range row {
			// Devices is a map of exact, case-sensitive identities, not a struct.
			credential := false
			for _, name := range []string{"vless", "vless3", "vless4", "hy2", "naive", "anytls", "wg"} {
				if strings.EqualFold(key, name) {
					credential = true
				}
			}
			if credential {
				var credentials map[string]json.RawMessage
				if json.Unmarshal(value, &credentials) != nil || check(credentials) != nil {
					return ErrLegacyNormalize
				}
			}
		}
	}
	return nil
}

// json.Unmarshal replaces unpaired UTF-16 escapes with U+FFFD. Reject only
// malformed escapes, while preserving legitimate literal replacement characters.
func rejectLegacySurrogates(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return ErrLegacyNormalize
		}
		if raw[i] != 'u' {
			continue
		}
		if i+5 > len(raw) {
			return ErrLegacyNormalize
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return ErrLegacyNormalize
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return ErrLegacyNormalize
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+7 > len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return ErrLegacyNormalize
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return ErrLegacyNormalize
			}
			i += 6
		}
	}
	return nil
}

// Token traversal also rejects repeated object keys inside credentials/devices.
// encoding/json otherwise silently keeps the last value, losing source data.
func rejectDuplicateLegacyJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return ErrLegacyNormalize
		}
		token, err := decoder.Token()
		if err != nil {
			return ErrLegacyNormalize
		}
		delim, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for decoder.More() {
				token, err := decoder.Token()
				key, ok := token.(string)
				if err != nil || !ok || keys[key] {
					return ErrLegacyNormalize
				}
				keys[key] = true
				if value(depth+1) != nil {
					return ErrLegacyNormalize
				}
			}
		case '[':
			for decoder.More() {
				if value(depth+1) != nil {
					return ErrLegacyNormalize
				}
			}
		default:
			return ErrLegacyNormalize
		}
		end, err := decoder.Token()
		if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
			return ErrLegacyNormalize
		}
		return nil
	}
	if value(0) != nil {
		return ErrLegacyNormalize
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrLegacyNormalize
	}
	return nil
}

func NormalizeLegacyCustomers(raw []byte, capture LegacyXUICapture, box *controlplane.SecretBox, hmacKey []byte, options LegacyNormalizeOptions) (Snapshot, error) {
	stage := "CUSTOMER_DECODE"
	failed := func() (Snapshot, error) { return Snapshot{}, &legacyNormalizeFailure{stage: stage} }
	customers, err := DecodeLegacyCustomers(raw)
	if err != nil {
		return failed()
	}
	stage = "CAPTURE_BINDING"
	if box == nil || len(hmacKey) != 32 || options.Now.IsZero() || options.MaxCaptureAge <= 0 ||
		capture.SchemaVersion != 1 || capture.CustomersSHA256 != sha256Hex(raw) || capture.CapturedAt.IsZero() ||
		capture.CapturedAt.Unix() <= 0 || capture.CompletedAt.Before(capture.CapturedAt) || capture.CompletedAt.After(options.Now) ||
		options.Now.Sub(capture.CapturedAt) > options.MaxCaptureAge {
		return failed()
	}
	snapshot := Snapshot{FormatVersion: 2, SnapshotKind: "full", CapturedAt: capture.CapturedAt,
		ClusterHMACKeySHA256: sha256Hex(hmacKey), SourceHashes: map[string]string{
			"customers": sha256Hex(raw), "xui_capture": canonicalLegacyDigest(capture),
			"scope:" + LegacyCustomerPreparationScope: sha256Hex([]byte(LegacyCustomerPreparationScope)),
			"protocol_bindings":                       canonicalLegacyDigest(options.ProtocolBindings),
		}}
	// Every unsupported source domain must be accounted for. These facts remain
	// visible and digest-bound in the Snapshot; this output is never cutover proof.
	stage = "SOURCE_PRESENCE"
	if len(options.Sources) != 4 {
		return failed()
	}
	for _, domain := range []string{"orders", "trials", "settings", "principals"} {
		presence, exists := options.Sources[domain]
		if !exists {
			return failed()
		}
		switch presence.State {
		case "absent":
			if presence.SHA256 != "" {
				return failed()
			}
			snapshot.SourceHashes["legacy:"+domain+":absent"] = sha256Hex([]byte("absent"))
		case "present":
			if !validCanonicalSHA256(presence.SHA256) {
				return failed()
			}
			snapshot.SourceHashes["legacy:"+domain+":present-unconverted"] = presence.SHA256
		default:
			return failed()
		}
	}
	bindings := map[string]LegacyNodeCapture{}
	subIDs := map[string]bool{}
	stage = "XUI_BINDINGS"
	for _, binding := range capture.Bindings {
		key := binding.Login + "\x00" + binding.NodeID
		if _, exists := bindings[key]; exists || binding.Login == "" || binding.Server == "" ||
			(binding.NodeID != "S1" && binding.NodeID != "S3" && binding.NodeID != "S4") ||
			!validLegacyNodeCapture(binding, capture, options.Now, options.MaxCaptureAge) ||
			(binding.SubID != "" && subIDs[binding.NodeID+"\x00"+binding.SubID]) {
			return failed()
		}
		bindings[key] = binding
		if binding.SubID != "" {
			subIDs[binding.NodeID+"\x00"+binding.SubID] = true
		}
	}
	protocolNodes := map[string]string{}
	stage = "PROTOCOL_BINDINGS"
	for _, binding := range options.ProtocolBindings {
		key := binding.Protocol + "\x00" + binding.Server
		if (binding.Protocol != "hysteria2" && binding.Protocol != "naive" && binding.Protocol != "anytls" && binding.Protocol != "awg") ||
			binding.Server == "" || !legacyNodeID(binding.NodeID) || protocolNodes[key] != "" {
			return failed()
		}
		protocolNodes[key] = binding.NodeID
	}
	parentRows, parentIdentities := map[string]LegacyCustomer{}, map[string]ProductionCustomerIdentity{}
	parentLogins := map[string]LegacyCustomer{}
	planOptions := options.PlanOptions
	if options.Parent != nil {
		stage = "PARENT_BINDING"
		parent := *options.Parent
		parentScope, parentPreparation := parent.SourceHashes["scope:"+LegacyCustomerPreparationScope]
		if parent.SnapshotKind != "full" || !capture.CapturedAt.After(parent.CapturedAt) ||
			parent.ClusterHMACKeySHA256 != snapshot.ClusterHMACKeySHA256 ||
			parentPreparation == options.CompleteNativeImport ||
			(parentPreparation && parentScope != snapshot.SourceHashes["scope:"+LegacyCustomerPreparationScope]) ||
			(options.CompleteNativeImport && !nativeImportSourcesComplete(parent.SourceHashes)) {
			return failed()
		}
		stage = "PARENT_PROTECTION"
		if _, err := ValidateSnapshotProtection(ProtectionFromSnapshot(parent), box, hmacKey, trialSourceSalt(options.TrialSource)); err != nil {
			return failed()
		}
		stage = "PARENT_IDENTITY"
		if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(parent), box); err != nil {
			return failed()
		}
		stage = "PARENT_PLAN"
		_, report := Plan(parent, options.PlanOptions)
		if len(report.Blockers) != 0 {
			return failed()
		}
		secrets := map[string]LegacyEncryptedSecret{}
		for _, secret := range parent.EncryptedSecrets {
			secrets[secret.SecretID] = secret
		}
		for _, row := range parent.Customers {
			stage = "PARENT_CUSTOMER_IDENTITY"
			identity, err := openProductionIdentity(box, row.SourceKey, secrets[row.IdentitySecretRef])
			if err != nil || parentLogins[identity.Customer.Login].SourceKey != "" {
				return failed()
			}
			parentRows[row.SourceKey], parentIdentities[row.SourceKey] = row, identity
			parentLogins[identity.Customer.Login] = row
		}
		snapshot.SnapshotKind, snapshot.ParentSourceDigest = "delta", digestSnapshot(parent)
		planOptions.ParentSnapshot, planOptions.AppliedParentDigest = options.Parent, snapshot.ParentSourceDigest
	}
	usedBindings, seenSources := map[string]bool{}, map[string]bool{}
	for _, customer := range customers {
		stage = "CUSTOMER_XUI_MATCH"
		login, _ := controlplane.CanonicalLoginKey(customer.Login)
		canonicalHMAC := box.LookupHMAC("customer-login", []byte(login))
		loginHMAC := box.LookupHMAC(controlplane.LegacyExactCustomerLoginHMACDomain, []byte(customer.Login))
		sourceKey := controlplane.LegacyExactCustomerSourcePrefix + canonicalHMAC + ":" + loginHMAC
		secretRef := "identity:" + loginHMAC
		// A cumulative capture preserves each authenticated parent's identity
		// mode and mapping. It never turns an existing canonical account into a
		// new exact account or changes the original case-sensitive login.
		if prior, exists := parentLogins[customer.Login]; exists {
			sourceKey, loginHMAC, secretRef = prior.SourceKey, prior.LoginKeyHMAC, prior.IdentitySecretRef
		}
		identity := ProductionCustomerIdentity{SchemaVersion: 1, Customer: customer, Generation: 1, NodeSubIDs: map[string]string{}}
		nodes := map[string]bool{}
		for nodeID, creds := range legacyVLESSNodes(customer) {
			key := customer.Login + "\x00" + nodeID
			binding, exists := bindings[key]
			if !exists || binding.UUID != creds.uuid || binding.Server != creds.server {
				return failed()
			}
			if binding.ObservedAbsent != nil {
				if identity.ObservedAbsent == nil {
					identity.ObservedAbsent = map[string]controlplane.LegacyXUIAbsenceEvidence{}
				}
				identity.ObservedAbsent[nodeID] = *binding.ObservedAbsent
			} else {
				identity.NodeSubIDs[nodeID] = binding.SubID
			}
			nodes[nodeID], usedBindings[key] = true, true
		}
		if prior, exists := parentIdentities[sourceKey]; exists {
			for node, evidence := range prior.ObservedAbsent {
				if _, stillAbsent := identity.ObservedAbsent[node]; !stillAbsent {
					return failed()
				}
				identity.ObservedAbsent[node] = evidence
			}
		}
		identity.SubID = identity.NodeSubIDs["S1"]
		stage = "CUSTOMER_CREDENTIALS"
		credentials, err := productionCredentials(identity)
		if err != nil {
			return failed()
		}
		protocols := make([]string, 0, len(credentials))
		for protocol := range credentials {
			protocols = append(protocols, protocol)
		}
		sort.Strings(protocols)
		stage = "CUSTOMER_PROTOCOL_MAP"
		for protocol, server := range legacyOtherServers(customer) {
			nodeID := protocolNodes[protocol+"\x00"+server]
			if nodeID == "" {
				return failed()
			}
			nodes[nodeID] = true
		}
		nodeIDs := make([]string, 0, len(nodes))
		for nodeID := range nodes {
			nodeIDs = append(nodeIDs, nodeID)
		}
		sort.Strings(nodeIDs)
		stage = "CUSTOMER_IDENTITY"
		fingerprint, err := json.Marshal(credentials)
		if err != nil {
			return failed()
		}
		row := LegacyCustomer{SourceKey: sourceKey, Login: customer.Login, LoginKeyHMAC: loginHMAC,
			TokenHMAC:                 box.LookupHMAC("subscription-token", []byte(customer.SubToken)),
			CredentialFingerprintHMAC: box.LookupHMAC("customer-credentials", fingerprint),
			IdentitySecretRef:         secretRef, ProtocolTags: protocols, NodeIDs: nodeIDs,
			ExpiresAtUnix: customer.Expires.Unix(), Generation: 1, Status: "active"}
		zeroBytes(fingerprint)
		if customer.VLESS != nil {
			row.UUIDHMAC = box.LookupHMAC("customer-uuid", []byte(customer.VLESS.UUID))
			if identity.SubID != "" {
				row.SubIDHMAC = box.LookupHMAC("subscription-id", []byte(identity.SubID))
			}
		}
		if customer.Disabled {
			row.Status = "suspended"
		}
		if prior, exists := parentRows[sourceKey]; exists {
			identity.Generation, row.Generation = prior.Generation, prior.Generation
			if canonicalLegacyDigest(row) != canonicalLegacyDigest(prior) || canonicalLegacyDigest(identity) != canonicalLegacyDigest(parentIdentities[sourceKey]) {
				if prior.Generation == math.MaxInt64 {
					return failed()
				}
				identity.Generation, row.Generation = prior.Generation+1, prior.Generation+1
			}
		}
		if validateProductionIdentity(box, row, identity) != nil {
			return failed()
		}
		plaintext, err := json.Marshal(identity)
		if err != nil {
			return failed()
		}
		scope := controlplane.SecretScope{OwnerType: "customer", OwnerID: sourceKey, Field: "identity", Kind: "customer-identity"}
		envelope, sealErr := box.Seal(scope, plaintext)
		secret := LegacyEncryptedSecret{SecretID: row.IdentitySecretRef, OwnerType: scope.OwnerType, OwnerSourceKey: sourceKey,
			Field: scope.Field, Kind: scope.Kind, KeyVersion: envelope.KeyVersion,
			NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce), CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: sha256Hex(plaintext)}
		zeroBytes(plaintext)
		if sealErr != nil {
			return failed()
		}
		snapshot.Customers = append(snapshot.Customers, row)
		snapshot.EncryptedSecrets = append(snapshot.EncryptedSecrets, secret)
		stage = "CUSTOMER_ABSENCE_PROOF"
		if appendLegacyNodeAbsences(&snapshot, row, identity, box, options.Parent) != nil {
			return failed()
		}
		seenSources[sourceKey] = true
	}
	stage = "XUI_BINDING_COVERAGE"
	if len(usedBindings) != len(bindings) {
		return failed()
	}
	// Removal requires explicit deletion semantics; a cumulative capture must not
	// accidentally turn a disappeared source record into a retained target user.
	stage = "PARENT_CUSTOMER_COVERAGE"
	for sourceKey := range parentRows {
		if !seenSources[sourceKey] {
			return failed()
		}
	}
	stage = "TRIAL_CONVERSION"
	if normalizeLegacyTrialSource(&snapshot, options.TrialSource, box, options.Parent) != nil {
		return failed()
	}
	stage = "ORDER_CONVERSION"
	if normalizeLegacyOrderSource(&snapshot, options.OrderSource, box, options.Parent) != nil {
		return failed()
	}
	stage = "RUNTIME_CONVERSION"
	if normalizeLegacyRuntimeSource(&snapshot, raw, options.RuntimeSource, box, options.Now, options.MaxCaptureAge, options.Parent) != nil {
		return failed()
	}
	if options.CompleteNativeImport {
		stage = "COMPLETE_SOURCE_SET"
		if options.OrderSource == nil || options.TrialSource == nil || options.RuntimeSource == nil || len(options.RuntimeSource.RawOTAAbsence) == 0 || !nativeImportSourcesComplete(snapshot.SourceHashes) {
			return failed()
		}
		protection := ProtectionFromSnapshot(snapshot, options.Parent)
		stage = "COMPLETE_PROTECTION"
		if _, err := ValidateSnapshotProtection(protection, box, hmacKey, trialSourceSalt(options.TrialSource)); err != nil {
			return failed()
		}
		stage = "COMPLETE_IDENTITY"
		if _, err := ValidateProductionCustomerIdentities(protection, box); err != nil {
			return failed()
		}
		// This flag lifts only the preparatory apply prohibition. Planning,
		// protected publication and live cutover gates remain independent.
		delete(snapshot.SourceHashes, "scope:"+LegacyCustomerPreparationScope)
	}
	sort.Slice(snapshot.Customers, func(i, j int) bool { return snapshot.Customers[i].SourceKey < snapshot.Customers[j].SourceKey })
	sort.Slice(snapshot.EncryptedSecrets, func(i, j int) bool {
		return snapshot.EncryptedSecrets[i].SecretID < snapshot.EncryptedSecrets[j].SecretID
	})
	stage = "SNAPSHOT_ENCODING"
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return failed()
	}
	stage = "SNAPSHOT_DECODE"
	_, err = DecodeSnapshot(encoded)
	zeroBytes(encoded)
	if err != nil {
		return failed()
	}
	stage = "SNAPSHOT_PLAN"
	_, report := Plan(snapshot, planOptions)
	if len(report.Blockers) != 0 {
		return failed()
	}
	protection := ProtectionFromSnapshot(snapshot, options.Parent)
	stage = "SNAPSHOT_PROTECTION"
	if _, err := ValidateSnapshotProtection(protection, box, hmacKey, trialSourceSalt(options.TrialSource)); err != nil {
		return failed()
	}
	stage = "SNAPSHOT_IDENTITY"
	if _, err := ValidateProductionCustomerIdentities(protection, box); err != nil {
		return failed()
	}
	return snapshot, nil
}

func nativeImportSourcesComplete(hashes map[string]string) bool {
	for _, key := range []string{"customers", "xui_capture", "protocol_bindings", legacyOrdersConvertedSource, legacyTrialConvertedSource, "legacy:settings:runtime-converted-v1", "legacy:principals:runtime-converted-v1", "legacy:ota:absent-v1"} {
		if !validCanonicalSHA256(hashes[key]) {
			return false
		}
	}
	if hashes["legacy:settings:runtime-converted-v1"] != hashes["legacy:principals:runtime-converted-v1"] {
		return false
	}
	for key, value := range hashes {
		if !validCanonicalSHA256(value) {
			return false
		}
		switch key {
		case "customers", "xui_capture", "protocol_bindings", "source_inventory", "scope:" + LegacyCustomerPreparationScope,
			legacyOrdersConvertedSource, legacyTrialConvertedSource, "legacy:settings:runtime-converted-v1", "legacy:principals:runtime-converted-v1", "legacy:ota:absent-v1", legacyRuntimeCurrentCapture, legacyRuntimeCurrentOTA:
		default:
			if !strings.HasPrefix(key, legacyOrderAliasPrefix) {
				return false
			}
		}
	}
	return true
}

type legacyVLESSBinding struct{ server, uuid string }

func legacyVLESSNodes(customer legacystore.Customer) map[string]legacyVLESSBinding {
	nodes := map[string]legacyVLESSBinding{}
	if customer.VLESS != nil {
		nodes["S1"] = legacyVLESSBinding{customer.VLESS.Server, customer.VLESS.UUID}
	}
	if customer.VLESS3 != nil {
		nodes["S3"] = legacyVLESSBinding{customer.VLESS3.Server, customer.VLESS3.UUID}
	}
	if customer.VLESS4 != nil {
		nodes["S4"] = legacyVLESSBinding{customer.VLESS4.Server, customer.VLESS4.UUID}
	}
	return nodes
}

func legacyOtherServers(customer legacystore.Customer) map[string]string {
	servers := map[string]string{}
	if customer.Hy2 != nil {
		servers["hysteria2"] = customer.Hy2.Server
	}
	if customer.Naive != nil {
		servers["naive"] = customer.Naive.Server
	}
	if customer.AnyTLS != nil {
		servers["anytls"] = customer.AnyTLS.Server
	}
	if customer.WG != nil {
		servers["awg"] = customer.WG.Server
	}
	return servers
}

func legacyNodeID(node string) bool {
	return node == "S1" || node == "S2" || node == "S3" || node == "S4"
}
