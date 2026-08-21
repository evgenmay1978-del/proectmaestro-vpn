package importer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func decodeFixture(t *testing.T, name string) Snapshot {
	t.Helper()
	snapshot, err := DecodeSnapshot(fixture(t, name))
	if err != nil {
		t.Fatalf("DecodeSnapshot(%s): %v", name, err)
	}
	return snapshot
}

func testPlanOptions() PlanOptions {
	return PlanOptions{
		Namespace:             "maestro-legacy-v1",
		SupportedBotSchemas:   []string{"bot-schema-v1"},
		SupportedProtocolTags: []string{"vless", "hysteria2", "anytls", "naive", "wdtt", "olcrtc"},
		SupportedNodeIDs:      []string{"S1", "S2", "S3", "S4"},
	}
}

func TestDecodeSnapshotRejectsTruncatedJSONWithoutEchoingInput(t *testing.T) {
	privateMarker := "must-not-appear-in-error"
	_, err := DecodeSnapshot([]byte(`{"format_version":1,"marker":"` + privateMarker))
	if err == nil {
		t.Fatal("truncated JSON was accepted")
	}
	if strings.Contains(err.Error(), privateMarker) {
		t.Fatalf("decoder error echoed input: %v", err)
	}
}

func TestPlanPreservesExactLegacyIdentityBytesAndExpiry(t *testing.T) {
	snapshot := decodeFixture(t, "customers-valid.json")
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	if len(plan.Customers) != 1 || len(plan.EncryptedSecrets) != 1 {
		t.Fatalf("plan counts = customers:%d secrets:%d", len(plan.Customers), len(plan.EncryptedSecrets))
	}
	customer := plan.Customers[0]
	if customer.DisplayLogin != "CaseSensitiveUser" || customer.ExpiresAtUnix != 2_100_000 ||
		customer.Generation != 7 || customer.IdentitySecretRef != "secret-customer-1" {
		t.Fatalf("legacy customer bytes/expiry changed: %#v", customer)
	}
	secret := plan.EncryptedSecrets[0]
	if secret.NonceB64 != "AAECAwQFBgcICQoL" || secret.CiphertextB64 != "c3ludGhldGljLWNpcGhlcnRleHQ=" || secret.KeyVersion != 1 {
		t.Fatalf("protected identity envelope changed: %#v", secret)
	}
}

func TestPlanRequiresExplicitSupportedProtocolAndNodeTopology(t *testing.T) {
	snapshot := decodeFixture(t, "customers-valid.json")
	snapshot.Customers[0].ProtocolTags = []string{"vless", "hysteria2"}
	snapshot.Customers[0].NodeIDs = []string{"S4", "S2", "S1", "S3"}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	wantProtocols := []string{"hysteria2", "vless"}
	wantNodes := []string{"S1", "S2", "S3", "S4"}
	if !reflect.DeepEqual(plan.Customers[0].ProtocolTags, wantProtocols) {
		t.Fatalf("protocol tags = %v, want %v", plan.Customers[0].ProtocolTags, wantProtocols)
	}
	if !reflect.DeepEqual(plan.Customers[0].NodeIDs, wantNodes) {
		t.Fatalf("node ids = %v, want %v", plan.Customers[0].NodeIDs, wantNodes)
	}
}

func TestPlanBlocksMissingUnsupportedOrDuplicateTopology(t *testing.T) {
	base := decodeFixture(t, "customers-valid.json")
	cases := []struct {
		name      string
		code      string
		protocols []string
		nodes     []string
	}{
		{"missing protocols", "missing_customer_protocols", nil, []string{"S1"}},
		{"missing nodes", "missing_customer_nodes", []string{"vless"}, nil},
		{"duplicate protocol", "duplicate_customer_protocol", []string{"vless", "vless"}, []string{"S1"}},
		{"duplicate node", "duplicate_customer_node", []string{"vless"}, []string{"S1", "S1"}},
		{"unsupported protocol", "unsupported_customer_protocol", []string{"unknown"}, []string{"S1"}},
		{"unsupported node", "unsupported_customer_node", []string{"vless"}, []string{"S9"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := base
			snapshot.Customers = append([]LegacyCustomer(nil), base.Customers...)
			snapshot.Customers[0].ProtocolTags = tc.protocols
			snapshot.Customers[0].NodeIDs = tc.nodes
			_, report := Plan(snapshot, testPlanOptions())
			if !hasBlockerCode(report.Blockers, tc.code) {
				t.Fatalf("blockers=%#v, want %s", report.Blockers, tc.code)
			}
		})
	}
}

func TestPlanReportsEveryCollisionInStableOrder(t *testing.T) {
	snapshot := decodeFixture(t, "collisions.json")
	_, first := Plan(snapshot, testPlanOptions())
	_, second := Plan(snapshot, testPlanOptions())
	want := []string{
		"credential_collision", "expiry_contradiction", "login_collision",
		"sub_id_collision", "token_hmac_collision", "uuid_collision",
	}
	got := make([]string, len(first.Blockers))
	for i, blocker := range first.Blockers {
		got[i] = blocker.Code
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blockers = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(first.Blockers, second.Blockers) {
		t.Fatalf("blocker order is unstable: %#v != %#v", first.Blockers, second.Blockers)
	}
}

func TestPendingCreditedPreservesExpiryWithoutSecondCredit(t *testing.T) {
	snapshot := decodeFixture(t, "orders-pending-credited.json")
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 || len(plan.Orders) != 2 {
		t.Fatalf("plan/report = %#v / %#v", plan, report)
	}
	credited := plan.Orders[0]
	if credited.PaymentState != "confirmed" || credited.ProvisioningState != "pending" ||
		credited.ResultExpiresAtUnix != 2_200_000 || !reflect.DeepEqual(credited.AuditMarkers, []string{"legacy_credit_preserved"}) {
		t.Fatalf("credited order was transformed incorrectly: %#v", credited)
	}
	uncredited := plan.Orders[1]
	if uncredited.ImportState != "created" || uncredited.ResultExpiresAtUnix != 0 {
		t.Fatalf("uncredited pending order was credited: %#v", uncredited)
	}
}

func TestPlanPreservesCanonicalLegacyOrderTerms(t *testing.T) {
	snapshot := decodeFixture(t, "orders-pending-credited.json")
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 || len(plan.Orders) != 2 {
		t.Fatalf("plan/report = %#v / %#v", plan, report)
	}
	order := plan.Orders[0]
	if order.BuyerScope != "customer_login" ||
		order.BuyerKeyHMAC != "1010101010101010101010101010101010101010101010101010101010101010" ||
		order.TariffVersionID != "tariff_1m_v1" || order.AmountMinor != 40_000 ||
		order.Currency != "RUB" || order.DurationDays != 30 || order.PaymentCode != "MCRD1" ||
		order.CreatedAtUnix != 1_000_000 || order.ExpiresAtUnix != 1_086_400 ||
		order.ResultGeneration != 9 {
		t.Fatalf("canonical legacy order terms changed: %#v", order)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if bytes.Contains(encoded, []byte("MCRD1")) {
		t.Fatalf("redacted report leaked payment code: %s", encoded)
	}
}

func TestPlanOperationsBindCustomerSecretAndOrderIdentity(t *testing.T) {
	snapshot := decodeFixture(t, "orders-pending-credited.json")
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	var customerOperation, orderOperation ApplyOperation
	for _, operation := range operations {
		switch {
		case operation.Entity == "customer":
			customerOperation = operation
		case operation.Entity == "order" && operation.Key == "legacy-order-credited":
			orderOperation = operation
		case operation.Entity == "encrypted_secret" && operation.Key == "secret-order-owner":
			t.Fatal("customer secret remained a detached generic operation")
		}
	}
	if len(customerOperation.CanonicalJSON) == 0 || len(orderOperation.CanonicalJSON) == 0 {
		t.Fatalf("missing canonical operations: %#v", operations)
	}
	var customerPayload struct {
		Customer       PlannedCustomer       `json:"customer"`
		IdentitySecret LegacyEncryptedSecret `json:"identity_secret"`
	}
	if err := json.Unmarshal(customerOperation.CanonicalJSON, &customerPayload); err != nil {
		t.Fatalf("decode customer operation: %v", err)
	}
	if customerPayload.Customer.InternalID != plan.Customers[0].InternalID ||
		customerPayload.IdentitySecret.SecretID != "secret-order-owner" {
		t.Fatalf("customer operation is not owner-bound: %#v", customerPayload)
	}
	var orderPayload struct {
		Order struct {
			CustomerInternalID string `json:"customer_internal_id"`
		} `json:"order"`
	}
	if err := json.Unmarshal(orderOperation.CanonicalJSON, &orderPayload); err != nil {
		t.Fatalf("decode order operation: %v", err)
	}
	if orderPayload.Order.CustomerInternalID != plan.Customers[0].InternalID {
		t.Fatalf("order customer internal id = %q, want %q", orderPayload.Order.CustomerInternalID, plan.Customers[0].InternalID)
	}
}

func TestPlanOperationsBindSettingAndPrincipalSecrets(t *testing.T) {
	snapshot := decodeFixture(t, "settings-principals-v1.json")
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	var settingOperation, principalOperation ApplyOperation
	for _, operation := range operations {
		switch {
		case operation.Entity == "setting" && operation.Key == "telegram":
			settingOperation = operation
		case operation.Entity == "principal" && operation.Key == "principal-owner":
			principalOperation = operation
		case operation.Entity == "encrypted_secret":
			t.Fatalf("protected owner secret remained detached: %s", operation.Key)
		}
	}
	var settingPayload struct {
		Setting LegacySetting          `json:"setting"`
		Secret  *LegacyEncryptedSecret `json:"secret"`
	}
	if err := json.Unmarshal(settingOperation.CanonicalJSON, &settingPayload); err != nil {
		t.Fatalf("decode setting operation: %v", err)
	}
	if settingPayload.Setting.Key != "telegram" || settingPayload.Secret == nil ||
		settingPayload.Secret.SecretID != "secret-bot-token" {
		t.Fatalf("setting secret is not owner-bound: %#v", settingPayload)
	}
	var principalPayload struct {
		Principal        map[string]any       `json:"principal"`
		CredentialSecret LegacyEncryptedSecret `json:"credential_secret"`
	}
	if err := json.Unmarshal(principalOperation.CanonicalJSON, &principalPayload); err != nil {
		t.Fatalf("decode principal operation: %v", err)
	}
	wantPrincipalID := deterministicID(testPlanOptions().Namespace, "principal", "principal-owner")
	if principalPayload.Principal["internal_id"] != wantPrincipalID ||
		principalPayload.CredentialSecret.SecretID != "secret-panel-verifier" {
		t.Fatalf("principal secret is not owner-bound: %#v", principalPayload)
	}
}

func TestUnsupportedBotSnapshotIsBlocking(t *testing.T) {
	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	snapshot.BotBindings[0].SchemaFingerprint = "unknown-live-schema"
	_, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 1 || report.Blockers[0].Code != "unsupported_bot_schema" {
		t.Fatalf("unsupported bot schema blockers = %#v", report.Blockers)
	}
}

func TestBotCredentialRotationChainMustBeLinearAndReachCurrentRoute(t *testing.T) {
	valid := decodeFixture(t, "bot-bindings-v1.json")
	botIdentity := valid.BotBindings[0].BotIdentityHMAC
	firstFingerprint := valid.BotBindings[0].TokenFingerprintHMAC
	middleFingerprint := strings.Repeat("3", 64)
	currentFingerprint := strings.Repeat("4", 64)
	valid.BotBindings[0].TokenFingerprintHMAC = currentFingerprint
	valid.BotBindings[0].CredentialVersion = 3
	valid.BotCredentialRotations = []LegacyBotCredentialRotation{
		{
			BotIdentityHMAC: botIdentity,
			OldTokenFingerprintHMAC: firstFingerprint,
			NewTokenFingerprintHMAC: middleFingerprint,
			OldCredentialVersion: 1,
			NewCredentialVersion: 2,
			AuditDigest: strings.Repeat("5", 64),
		},
		{
			BotIdentityHMAC: botIdentity,
			OldTokenFingerprintHMAC: middleFingerprint,
			NewTokenFingerprintHMAC: currentFingerprint,
			OldCredentialVersion: 2,
			NewCredentialVersion: 3,
			AuditDigest: strings.Repeat("6", 64),
		},
	}
	if _, report := Plan(valid, testPlanOptions()); len(report.Blockers) != 0 {
		t.Fatalf("valid rotation chain blockers = %#v", report.Blockers)
	}

	fork := decodeFixture(t, "bot-bindings-v1.json")
	fork.BotBindings[0].TokenFingerprintHMAC = currentFingerprint
	fork.BotBindings[0].CredentialVersion = 3
	fork.BotCredentialRotations = []LegacyBotCredentialRotation{
		{
			BotIdentityHMAC: botIdentity,
			OldTokenFingerprintHMAC: firstFingerprint,
			NewTokenFingerprintHMAC: middleFingerprint,
			OldCredentialVersion: 1,
			NewCredentialVersion: 2,
			AuditDigest: strings.Repeat("5", 64),
		},
		{
			BotIdentityHMAC: botIdentity,
			OldTokenFingerprintHMAC: firstFingerprint,
			NewTokenFingerprintHMAC: currentFingerprint,
			OldCredentialVersion: 1,
			NewCredentialVersion: 3,
			AuditDigest: strings.Repeat("6", 64),
		},
	}
	if _, report := Plan(fork, testPlanOptions()); !hasBlockerCode(report.Blockers, "bot_credential_rotation_fork") {
		t.Fatalf("forked rotation chain blockers = %#v", report.Blockers)
	}

	routeMismatch := valid
	routeMismatch.BotBindings = append([]LegacyBotBinding(nil), valid.BotBindings...)
	routeMismatch.BotBindings[0].TokenFingerprintHMAC = strings.Repeat("7", 64)
	if _, report := Plan(routeMismatch, testPlanOptions()); !hasBlockerCode(report.Blockers, "bot_credential_rotation_route_mismatch") {
		t.Fatalf("route mismatch blockers = %#v", report.Blockers)
	}
}

func hasBlockerCode(blockers []Blocker, code string) bool {
	for _, blocker := range blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

func TestEncryptedSecretIdentityCollisionIsBlocking(t *testing.T) {
	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	first := standaloneEncryptedSecret()
	conflicting := first
	conflicting.SHA256 = strings.Repeat("b", 64)
	snapshot.EncryptedSecrets = []LegacyEncryptedSecret{first, conflicting}

	_, report := Plan(snapshot, testPlanOptions())
	if !hasBlockerCode(report.Blockers, "encrypted_secret_collision") {
		t.Fatalf("encrypted secret collision blockers = %#v", report.Blockers)
	}
}

func TestTrialIdentityCollisionIsBlocking(t *testing.T) {
	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	first := legacyTrialFixture()
	conflicting := first
	conflicting.SourceKey = "legacy-trial-conflict"
	conflicting.CurrentHMAC = strings.Repeat("4", 64)
	snapshot.Trials = []LegacyTrial{first, conflicting}
	snapshot.LegacyTrialSaltSHA256 = protectedTrialImportFixture().SaltSHA256

	_, report := Plan(snapshot, testPlanOptions())
	if !hasBlockerCode(report.Blockers, "trial_identity_collision") {
		t.Fatalf("trial identity collision blockers = %#v", report.Blockers)
	}
}

func TestRequiredSettingsPrincipalsAndEncryptedSecretsArePreserved(t *testing.T) {
	snapshot := decodeFixture(t, "settings-principals-v1.json")
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	if len(plan.Settings) != 2 || len(plan.Principals) != 1 || len(plan.EncryptedSecrets) != 2 {
		t.Fatalf("settings/principals/secrets counts changed: %#v", report.Counts)
	}
	wantOTA := []byte(`{"versionCode":154,"versionName":"1.5.4","sha256":"` + strings.Repeat("5", 64) + `","size":12345678}`)
	if !bytes.Equal(plan.Settings[0].PublicValueJSON, wantOTA) ||
		!reflect.DeepEqual(plan.Principals[0].Roles, []string{"owner"}) {
		t.Fatalf("public setting or roles changed: %#v / %#v", plan.Settings, plan.Principals)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, forbidden := range []string{"c3ludGhldGljLXBhbmVsLXZlcmlmaWVy", "c3ludGhldGljLWJvdC10b2tlbg=="} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("immutable report leaked protected ciphertext: %s", encoded)
		}
	}
}

func TestMissingLegacyPrincipalSecretBlocksApply(t *testing.T) {
	snapshot := decodeFixture(t, "settings-principals-v1.json")
	snapshot.EncryptedSecrets = snapshot.EncryptedSecrets[1:]
	_, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 1 || report.Blockers[0].Code != "missing_principal_secret" {
		t.Fatalf("missing principal secret blockers = %#v", report.Blockers)
	}
}

func TestDecodeSnapshotAcceptsProtectionBoundV2(t *testing.T) {
	snapshot := decodeFixture(t, "customers-valid.json")
	snapshot.FormatVersion = 2
	snapshot.ClusterHMACKeySHA256 = strings.Repeat("a", 64)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSnapshot(encoded)
	if err != nil {
		t.Fatalf("DecodeSnapshot(v2): %v", err)
	}
	if decoded.FormatVersion != 2 ||
		decoded.ClusterHMACKeySHA256 != snapshot.ClusterHMACKeySHA256 {
		t.Fatalf("decoded protection metadata = %#v", decoded)
	}
}

func TestDecodeSnapshotV2RequiresCanonicalProtectionDigests(t *testing.T) {
	base := decodeFixture(t, "customers-valid.json")
	base.FormatVersion = 2
	base.ClusterHMACKeySHA256 = strings.Repeat("a", 64)

	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"legacy format", func(snapshot *Snapshot) {
			snapshot.FormatVersion = 1
		}},
		{"missing cluster hmac digest", func(snapshot *Snapshot) {
			snapshot.ClusterHMACKeySHA256 = ""
		}},
		{"uppercase cluster hmac digest", func(snapshot *Snapshot) {
			snapshot.ClusterHMACKeySHA256 = strings.Repeat("A", 64)
		}},
		{"nonhex cluster hmac digest", func(snapshot *Snapshot) {
			snapshot.ClusterHMACKeySHA256 = strings.Repeat("z", 64)
		}},
		{"missing trial salt digest", func(snapshot *Snapshot) {
			snapshot.Trials = []LegacyTrial{{
				SourceKey:        "decode-trial-v2",
				LegacyAnchorHMAC: strings.Repeat("1", 64),
				CurrentHMAC:      strings.Repeat("2", 64),
				ExpiresAtUnix:    2_100_100,
			}}
		}},
		{"unexpected trial salt digest", func(snapshot *Snapshot) {
			snapshot.LegacyTrialSaltSHA256 = strings.Repeat("b", 64)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := base
			tc.mutate(&snapshot)
			encoded, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeSnapshot(encoded); err == nil {
				t.Fatal("invalid protection metadata was accepted")
			}
		})
	}
}

func TestPlanBindsTrialSaltDigestOnlyWhenTrialsExist(t *testing.T) {
	snapshot := decodeFixture(t, "customers-valid.json")
	snapshot.Trials = []LegacyTrial{{
		SourceKey:        "trial-protection-v2",
		LegacyAnchorHMAC: strings.Repeat("1", 64),
		CurrentHMAC:      strings.Repeat("2", 64),
		ExpiresAtUnix:    2_100_100,
	}}
	snapshot.LegacyTrialSaltSHA256 = strings.Repeat("b", 64)
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("blockers=%#v", report.Blockers)
	}
	if plan.LegacyTrialSaltSHA256 != snapshot.LegacyTrialSaltSHA256 {
		t.Fatalf("salt digest was not preserved")
	}

	snapshot.Trials = nil
	_, report = Plan(snapshot, testPlanOptions())
	if !hasBlockerCode(report.Blockers, "unexpected_legacy_trial_salt_digest") {
		t.Fatalf("blockers=%#v", report.Blockers)
	}
}

func TestProtectionMetadataChangesSourceAndPlanDigests(t *testing.T) {
	left := decodeFixture(t, "customers-valid.json")
	right := left
	right.ClusterHMACKeySHA256 = strings.Repeat("c", 64)
	leftPlan, leftReport := Plan(left, testPlanOptions())
	rightPlan, rightReport := Plan(right, testPlanOptions())
	if len(leftReport.Blockers) != 0 || len(rightReport.Blockers) != 0 {
		t.Fatalf("left blockers=%#v right blockers=%#v", leftReport.Blockers, rightReport.Blockers)
	}
	if leftPlan.SourceDigest == rightPlan.SourceDigest ||
		leftPlan.PlanDigest == rightPlan.PlanDigest {
		t.Fatal("protection metadata was outside canonical digests")
	}
}
