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
		Namespace:          "maestro-legacy-v1",
		SupportedBotSchemas: []string{"bot-schema-v1"},
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

func TestRequiredSettingsPrincipalsAndEncryptedSecretsArePreserved(t *testing.T) {
	snapshot := decodeFixture(t, "settings-principals-v1.json")
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	if len(plan.Settings) != 2 || len(plan.Principals) != 1 || len(plan.EncryptedSecrets) != 2 {
		t.Fatalf("settings/principals/secrets counts changed: %#v", report.Counts)
	}
	if !bytes.Equal(plan.Settings[0].PublicValueJSON, []byte(`{"versionCode":154}`)) ||
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
