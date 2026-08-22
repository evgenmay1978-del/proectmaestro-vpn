package whitelistready

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const (
	testCommit = "0123456789abcdef0123456789abcdef01234567"
	testHashA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testHashB  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testTime   = "2026-08-22T00:00:00Z"
)

func TestValidateAcceptsCompleteOfflineFixtureBundle(t *testing.T) {
	catalog, evidence, matrix := validDocuments(t)
	assessment, err := Validate(catalog, evidence, matrix)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if assessment.HarnessStatus != HarnessPass || assessment.ReleaseReadiness != ReleaseNoGo ||
		assessment.EvidenceClass != EvidenceFixtureReplay || len(assessment.ValidatedSuites) != len(RequiredSuites()) {
		t.Fatalf("unexpected assessment: %#v", assessment)
	}
}

func TestValidateRejectsMalformedDocumentsWithFixedCodes(t *testing.T) {
	catalog, evidence, matrix := validDocuments(t)
	tests := []struct {
		name string
		cat  []byte
		evd  []byte
		mat  []byte
		code string
	}{
		{name: "oversize", cat: bytes.Repeat([]byte{' '}, MaxDocumentBytes+1), evd: evidence, mat: matrix, code: CodeDocumentTooLarge},
		{name: "invalid utf8", cat: []byte{0xff}, evd: evidence, mat: matrix, code: CodeInvalidUTF8},
		{name: "trailing json", cat: append(append([]byte(nil), catalog...), []byte("{}")...), evd: evidence, mat: matrix, code: CodeInvalidJSON},
		{name: "duplicate key", cat: []byte(`{"schema_version":1,"schema_version":1,"suites":[]}`), evd: evidence, mat: matrix, code: CodeDuplicateJSONKey},
		{name: "unknown field", cat: addJSONField(t, catalog, "unexpected", true), evd: evidence, mat: matrix, code: CodeUnknownField},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Validate(test.cat, test.evd, test.mat)
			assertReasonCode(t, err, test.code)
			if strings.Contains(err.Error(), "unexpected") || strings.Contains(err.Error(), "schema_version") {
				t.Fatalf("error echoed rejected data: %q", err)
			}
		})
	}
}

func TestValidateEnforcesCatalogShapeAndCanonicalHash(t *testing.T) {
	baseCatalog, baseEvidence, matrix := validModels(t)
	tests := []struct {
		name   string
		mutate func(*Catalog, *EvidenceBundle)
		code   string
	}{
		{name: "missing suite", mutate: func(c *Catalog, _ *EvidenceBundle) { c.Suites = c.Suites[1:] }, code: CodeSuiteSetInvalid},
		{name: "duplicate suite", mutate: func(c *Catalog, _ *EvidenceBundle) { c.Suites[1].ID = c.Suites[0].ID }, code: CodeDuplicateID},
		{name: "missing case", mutate: func(c *Catalog, _ *EvidenceBundle) { c.Suites[0].Cases = c.Suites[0].Cases[1:] }, code: CodeCaseSetInvalid},
		{name: "duplicate case", mutate: func(c *Catalog, _ *EvidenceBundle) { c.Suites[0].Cases[1].ID = c.Suites[0].Cases[0].ID }, code: CodeDuplicateID},
		{name: "wrong expected fact", mutate: func(c *Catalog, _ *EvidenceBundle) { *c.Suites[0].Cases[0].ExpectedFacts[0].IntegerValue = 2 }, code: CodeCatalogFactsInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := cloneJSON(t, baseCatalog)
			evidence := cloneJSON(t, baseEvidence)
			test.mutate(&catalog, &evidence)
			catalogRaw := marshalJSON(t, catalog)
			_, err := Validate(catalogRaw, marshalJSON(t, evidence), marshalJSON(t, matrix))
			assertReasonCode(t, err, test.code)
		})
	}

	compact := marshalJSON(t, baseCatalog)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		t.Fatal(err)
	}
	hashCompact, err := CanonicalCatalogSHA256(compact)
	if err != nil {
		t.Fatal(err)
	}
	hashPretty, err := CanonicalCatalogSHA256(pretty.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if hashCompact != hashPretty {
		t.Fatalf("canonical hash depends on formatting: compact=%s pretty=%s", hashCompact, hashPretty)
	}
}

func TestValidateEnforcesCandidateBindingAndFixtureEvidence(t *testing.T) {
	catalog, baseEvidence, matrix := validModels(t)
	tests := []struct {
		name   string
		mutate func(*EvidenceBundle)
		code   string
	}{
		{name: "malformed commit", mutate: func(e *EvidenceBundle) { e.Binding.CandidateCommit = "not-a-commit" }, code: CodeBindingInvalid},
		{name: "malformed hash", mutate: func(e *EvidenceBundle) { e.Binding.ArtifactSHA256 = "ABC" }, code: CodeBindingInvalid},
		{name: "wrong environment", mutate: func(e *EvidenceBundle) { e.Binding.Environment = "PRODUCTION" }, code: CodeOfflineFixtureRequired},
		{name: "non utc time", mutate: func(e *EvidenceBundle) { e.Binding.ObservedAt = "2026-08-22T03:00:00+03:00" }, code: CodeTimestampInvalid},
		{name: "observation binding mismatch", mutate: func(e *EvidenceBundle) { e.Observations[0].Binding.ConfigSHA256 = testHashA }, code: CodeBindingMismatch},
		{name: "strong fixture claim", mutate: func(e *EvidenceBundle) { e.Observations[0].EvidenceClass = EvidenceIsolatedRealBinary }, code: CodeEvidenceClassInvalid},
		{name: "fixture failed", mutate: func(e *EvidenceBundle) { e.Observations[0].VerificationState = VerificationFailed }, code: CodeObservationInvalid},
		{name: "wrong readiness", mutate: func(e *EvidenceBundle) { e.ReleaseReadiness = "GO" }, code: CodeReadinessInvalid},
		{name: "negative fact", mutate: func(e *EvidenceBundle) { n := int64(-1); e.Observations[0].Facts[0].IntegerValue = &n }, code: CodeFactInvalid},
		{name: "secret text", mutate: func(e *EvidenceBundle) {
			s := "token=must-not-echo"
			e.Observations[0].Facts = []Fact{{Name: "result", Kind: FactText, TextValue: &s}}
		}, code: CodeUnsafeString},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := cloneJSON(t, baseEvidence)
			test.mutate(&evidence)
			_, err := Validate(marshalJSON(t, catalog), marshalJSON(t, evidence), marshalJSON(t, matrix))
			assertReasonCode(t, err, test.code)
			if err != nil && strings.Contains(err.Error(), "must-not-echo") {
				t.Fatalf("error echoed secret-like data: %q", err)
			}
		})
	}
}

func TestValidateRequiresExactObservations(t *testing.T) {
	catalog, baseEvidence, matrix := validModels(t)
	tests := []struct {
		name   string
		mutate func(*EvidenceBundle)
		code   string
	}{
		{name: "missing", mutate: func(e *EvidenceBundle) { e.Observations = e.Observations[1:] }, code: CodeObservationSetInvalid},
		{name: "duplicate", mutate: func(e *EvidenceBundle) {
			e.Observations[1].SuiteID, e.Observations[1].CaseID = e.Observations[0].SuiteID, e.Observations[0].CaseID
		}, code: CodeDuplicateID},
		{name: "facts mismatch", mutate: func(e *EvidenceBundle) { *e.Observations[0].Facts[0].IntegerValue = 3 }, code: CodeObservationFactsMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := cloneJSON(t, baseEvidence)
			test.mutate(&evidence)
			_, err := Validate(marshalJSON(t, catalog), marshalJSON(t, evidence), marshalJSON(t, matrix))
			assertReasonCode(t, err, test.code)
		})
	}
}

func TestValidateRequiresUntestedFourClientMatrix(t *testing.T) {
	catalog, evidence, baseMatrix := validModels(t)
	tests := []struct {
		name   string
		mutate func(*ClientMatrix)
		code   string
	}{
		{name: "missing client", mutate: func(m *ClientMatrix) { m.Clients = m.Clients[1:] }, code: CodeClientSetInvalid},
		{name: "duplicate client", mutate: func(m *ClientMatrix) { m.Clients[1].ID = m.Clients[0].ID }, code: CodeDuplicateID},
		{name: "missing check", mutate: func(m *ClientMatrix) { m.Clients[0].Checks = m.Clients[0].Checks[1:] }, code: CodeClientCheckSetInvalid},
		{name: "fabricated status", mutate: func(m *ClientMatrix) { status := CompatibilitySupported; m.Clients[0].CompatibilityStatus = &status }, code: CodeCompatibilityUnobserved},
		{name: "passed without device evidence", mutate: func(m *ClientMatrix) { m.Clients[0].VerificationState = VerificationPassed }, code: CodeCompatibilityUnobserved},
		{name: "import only called supported", mutate: func(m *ClientMatrix) {
			status := CompatibilitySupported
			coreVersion := "xray-device-25.8.3"
			preset := "xhttp-get"
			m.Clients[0].VerificationState = VerificationPassed
			m.Clients[0].CompatibilityStatus = &status
			m.Clients[0].EvidenceClass = EvidenceDeviceObserved
			m.Clients[0].CoreVersion = &coreVersion
			m.Clients[0].Preset = &preset
			ref := "device-observation-1"
			m.Clients[0].EvidenceRef = &ref
			for index := range m.Clients[0].Checks {
				m.Clients[0].Checks[index].VerificationState = VerificationNotRun
			}
			m.Clients[0].Checks[0].VerificationState = VerificationPassed
			m.Clients[0].Checks[0].EvidenceClass = EvidenceDeviceObserved
			m.Clients[0].Checks[0].EvidenceRef = &ref
		}, code: CodeClientEvidenceIncomplete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matrix := cloneJSON(t, baseMatrix)
			test.mutate(&matrix)
			_, err := Validate(marshalJSON(t, catalog), marshalJSON(t, evidence), marshalJSON(t, matrix))
			assertReasonCode(t, err, test.code)
		})
	}
}

func TestReplaySelectsOnlyRequiredSuiteAndNeverUpgradesReadiness(t *testing.T) {
	catalog, evidence, matrix := validDocuments(t)
	assessment, err := Replay("edge_rotation", catalog, evidence, matrix)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.SelectedSuite != "edge_rotation" || assessment.ReleaseReadiness != ReleaseNoGo || assessment.EvidenceClass != EvidenceFixtureReplay {
		t.Fatalf("unexpected replay assessment: %#v", assessment)
	}
	_, err = Replay("edge_rotation*", catalog, evidence, matrix)
	assertReasonCode(t, err, CodeSuiteInvalid)
}

func validDocuments(t *testing.T) ([]byte, []byte, []byte) {
	t.Helper()
	catalog, evidence, matrix := validModels(t)
	return marshalJSON(t, catalog), marshalJSON(t, evidence), marshalJSON(t, matrix)
}

func validModels(t *testing.T) (Catalog, EvidenceBundle, ClientMatrix) {
	t.Helper()
	catalog := Catalog{SchemaVersion: SchemaVersion}
	for _, suiteID := range RequiredSuites() {
		suite := CatalogSuite{ID: suiteID}
		for _, caseID := range RequiredCaseIDs(suiteID) {
			suite.Cases = append(suite.Cases, CatalogCase{ID: caseID, ExpectedFacts: requiredFactsForTest(caseID)})
		}
		catalog.Suites = append(catalog.Suites, suite)
	}
	binding := CandidateBinding{
		CandidateCommit: testCommit,
		ArtifactSHA256:  testHashA,
		ConfigSHA256:    testHashB,
		CatalogSHA256:   mustCatalogHash(t, catalog),
		ToolVersion:     "task7-harness-1.0.0",
		CoreVersion:     "xray-fixture-25.8.3",
		Environment:     EnvironmentOfflineFixture,
		ObservedAt:      testTime,
	}
	evidence := EvidenceBundle{
		SchemaVersion:    SchemaVersion,
		Binding:          binding,
		EvidenceClass:    EvidenceFixtureReplay,
		HarnessStatus:    HarnessPass,
		ReleaseReadiness: ReleaseNoGo,
	}
	for _, suite := range catalog.Suites {
		for _, fixtureCase := range suite.Cases {
			evidence.Observations = append(evidence.Observations, Observation{
				SuiteID:           suite.ID,
				CaseID:            fixtureCase.ID,
				Binding:           binding,
				EvidenceClass:     EvidenceFixtureReplay,
				VerificationState: VerificationPassed,
				Facts:             cloneJSON(t, fixtureCase.ExpectedFacts),
			})
		}
	}
	matrix := ClientMatrix{Binding: binding, SchemaVersion: SchemaVersion, BaselineVersion: "1.0.157"}
	for _, clientID := range RequiredClients() {
		client := ClientRecord{ID: clientID, VerificationState: VerificationNotRun, EvidenceClass: EvidenceSchemaOnly}
		if clientID == "maestrovpn" {
			version := "1.0.157"
			client.AppVersion = &version
		}
		for _, checkID := range RequiredClientChecks() {
			client.Checks = append(client.Checks, ClientCheck{ID: checkID, VerificationState: VerificationNotRun, EvidenceClass: EvidenceSchemaOnly})
		}
		matrix.Clients = append(matrix.Clients, client)
	}
	return catalog, evidence, matrix
}

func requiredFactsForTest(caseID string) []Fact {
	integer := func(name string, value int64) Fact { return Fact{Name: name, Kind: FactInteger, IntegerValue: &value} }
	boolean := func(name string, value bool) Fact { return Fact{Name: name, Kind: FactBoolean, BooleanValue: &value} }
	text := func(name, value string) Fact { return Fact{Name: name, Kind: FactText, TextValue: &value} }
	switch caseID {
	case "body-1b":
		return []Fact{integer("body_bytes", 1), boolean("digest_match", true)}
	case "body-1kib":
		return []Fact{integer("body_bytes", 1024), boolean("digest_match", true)}
	case "body-64kib":
		return []Fact{integer("body_bytes", 65536), boolean("digest_match", true)}
	case "body-256kib":
		return []Fact{integer("body_bytes", 262144), boolean("digest_match", true)}
	case "body-typical":
		return []Fact{integer("body_bytes", 131072), boolean("digest_match", true)}
	case "body-max":
		return []Fact{integer("body_bytes", 1048576), boolean("digest_match", true)}
	case "auth-pass":
		return []Fact{text("authorization_result", "ALLOW")}
	case "sequence-pass":
		return []Fact{boolean("sequence_preserved", true)}
	case "cache-disabled":
		return []Fact{boolean("cache_disabled", true)}
	case "invalid-host-rejected", "invalid-path-rejected", "invalid-status-rejected":
		return []Fact{boolean("rejected", true)}
	case "latency-bounded":
		return []Fact{integer("latency_ms", 250)}
	case "retry-bounded":
		return []Fact{integer("retry_count", 2)}
	case "active-stream-5m":
		return []Fact{integer("duration_seconds", 300), boolean("bidirectional", true)}
	case "idle-90s-recovery":
		return []Fact{integer("idle_seconds", 90), boolean("recovered", true)}
	case "literal-edge-get":
		return []Fact{boolean("literal_edge_used", true)}
	case "counter-reset":
		return []Fact{integer("delta_bytes", 0), boolean("reset_detected", true)}
	case "stable-identity":
		return []Fact{boolean("identity_stable", true)}
	case "idempotent":
		return []Fact{integer("charge_events", 1)}
	case "duplicate-event":
		return []Fact{integer("applied_events", 1)}
	case "out-of-order":
		return []Fact{boolean("monotonic_total", true)}
	case "primary":
		return []Fact{integer("selected_rank", 1)}
	case "failover":
		return []Fact{integer("selected_rank", 2)}
	case "finite-fallback":
		return []Fact{integer("selected_rank", 3)}
	case "base64":
		return []Fact{boolean("base64_valid", true)}
	case "plain":
		return []Fact{boolean("plain_valid", true)}
	case "utf8":
		return []Fact{boolean("utf8_preserved", true)}
	case "escaping":
		return []Fact{boolean("escaping_preserved", true)}
	case "long-uri":
		return []Fact{boolean("long_uri_preserved", true)}
	case "qr":
		return []Fact{boolean("qr_round_trip", true)}
	case "refresh":
		return []Fact{boolean("refresh_stable", true)}
	case "dedup":
		return []Fact{boolean("deduplicated", true)}
	case "reimport":
		return []Fact{boolean("identity_preserved", true)}
	case "revocation":
		return []Fact{boolean("revoked_removed", true)}
	case "cache-invalidation":
		return []Fact{boolean("cache_invalidated", true)}
	default:
		return nil
	}
}

func mustCatalogHash(t *testing.T, catalog Catalog) string {
	t.Helper()
	hash, err := CanonicalCatalogSHA256(marshalJSON(t, catalog))
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func cloneJSON[T any](t *testing.T, value T) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(marshalJSON(t, value), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func addJSONField(t *testing.T, raw []byte, key string, value any) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object[key] = value
	return marshalJSON(t, object)
}

func assertReasonCode(t *testing.T, err error, expected string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s", expected)
	}
	reasoned, ok := err.(interface{ ReasonCode() string })
	if !ok || reasoned.ReasonCode() != expected {
		t.Fatalf("error=%v code=%q, want %q", err, reasonedCode(err), expected)
	}
}

func reasonedCode(err error) string {
	if value, ok := err.(interface{ ReasonCode() string }); ok {
		return value.ReasonCode()
	}
	return ""
}
