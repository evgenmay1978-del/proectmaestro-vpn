package whitelistready

import (
	"bytes"
	"testing"
)

func TestValidateRejectsCaseFoldedJSONKeys(t *testing.T) {
	catalog, evidence, matrix := validDocuments(t)
	upper := bytes.Replace(catalog, []byte(`"schema_version"`), []byte(`"SCHEMA_VERSION"`), 1)
	_, err := Validate(upper, evidence, matrix)
	assertReasonCode(t, err, CodeUnknownField)

	duplicateFolded := bytes.Replace(catalog, []byte(`"schema_version":1`), []byte(`"schema_version":1,"SCHEMA_VERSION":1`), 1)
	_, err = Validate(duplicateFolded, evidence, matrix)
	assertReasonCode(t, err, CodeUnknownField)
}

func TestValidateBindsClientMatrixToSameCandidate(t *testing.T) {
	catalog, evidence, matrix := validModels(t)
	matrix.Binding.ConfigSHA256 = testHashA
	_, err := Validate(marshalJSON(t, catalog), marshalJSON(t, evidence), marshalJSON(t, matrix))
	assertReasonCode(t, err, CodeBindingMismatch)
}

func TestValidateDerivesObservedClientAggregateFromChecks(t *testing.T) {
	catalog, evidence, baseMatrix := validModels(t)
	tests := []struct {
		name   string
		mutate func(*ClientRecord)
	}{
		{name: "failed aggregate cannot claim supported", mutate: func(client *ClientRecord) {
			makeObservedClient(client, EvidenceDeviceObserved)
			client.VerificationState = VerificationFailed
		}},
		{name: "aggregate class cannot exceed checks", mutate: func(client *ClientRecord) {
			makeObservedClient(client, EvidenceDeviceObserved)
			client.EvidenceClass = EvidenceProductionObserved
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matrix := cloneJSON(t, baseMatrix)
			test.mutate(&matrix.Clients[0])
			_, err := Validate(marshalJSON(t, catalog), marshalJSON(t, evidence), marshalJSON(t, matrix))
			assertReasonCode(t, err, CodeClientEvidenceIncomplete)
		})
	}
}

func TestValidateRequiresNullUnobservedExternalMetadata(t *testing.T) {
	catalog, evidence, baseMatrix := validModels(t)
	tests := []struct {
		name   string
		mutate func(*ClientRecord)
	}{
		{name: "external app version", mutate: func(client *ClientRecord) { value := "unknown"; client.AppVersion = &value }},
		{name: "core version", mutate: func(client *ClientRecord) { value := "unknown"; client.CoreVersion = &value }},
		{name: "preset", mutate: func(client *ClientRecord) { value := "unknown"; client.Preset = &value }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matrix := cloneJSON(t, baseMatrix)
			test.mutate(&matrix.Clients[1])
			_, err := Validate(marshalJSON(t, catalog), marshalJSON(t, evidence), marshalJSON(t, matrix))
			assertReasonCode(t, err, CodeCompatibilityUnobserved)
		})
	}
}

func TestSubscriptionCasesHaveDistinctRequiredFacts(t *testing.T) {
	seen := make(map[string]string)
	for _, caseID := range RequiredCaseIDs("subscription_escaping") {
		facts := requiredFacts(caseID)
		if len(facts) != 1 {
			t.Fatalf("case %s has %d facts, want one semantic fact", caseID, len(facts))
		}
		name := facts[0].Name
		if previous, duplicate := seen[name]; duplicate {
			t.Fatalf("cases %s and %s share generic fact %s", previous, caseID, name)
		}
		seen[name] = caseID
	}
}

func makeObservedClient(client *ClientRecord, class EvidenceClass) {
	appVersion := "1.0.158-task7-test"
	coreVersion := "xray-device-25.8.3"
	preset := "xhttp-get"
	status := CompatibilitySupported
	reference := "device-observation-1"
	client.AppVersion = &appVersion
	client.CoreVersion = &coreVersion
	client.Preset = &preset
	client.VerificationState = VerificationPassed
	client.CompatibilityStatus = &status
	client.EvidenceClass = class
	client.EvidenceRef = &reference
	for index := range client.Checks {
		client.Checks[index].VerificationState = VerificationPassed
		client.Checks[index].EvidenceClass = EvidenceDeviceObserved
		client.Checks[index].EvidenceRef = &reference
	}
}
