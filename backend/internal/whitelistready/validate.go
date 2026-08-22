package whitelistready

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxStringBytes = 256
	maxFactInteger = int64(1 << 40)
	maxJSONDepth   = 64
)

type validationError struct{ code string }

func (err validationError) Error() string      { return "white-list readiness validation failed" }
func (err validationError) ReasonCode() string { return err.code }

func invalid(code string) error { return validationError{code: code} }

func Validate(catalogRaw, evidenceRaw, matrixRaw []byte) (Assessment, error) {
	var catalog Catalog
	if err := decodeDocument(catalogRaw, &catalog); err != nil {
		return Assessment{}, err
	}
	if err := validateCatalog(catalog); err != nil {
		return Assessment{}, err
	}
	var evidence EvidenceBundle
	if err := decodeDocument(evidenceRaw, &evidence); err != nil {
		return Assessment{}, err
	}
	var matrix ClientMatrix
	if err := decodeDocument(matrixRaw, &matrix); err != nil {
		return Assessment{}, err
	}
	catalogHash, err := catalogSHA256(catalog)
	if err != nil {
		return Assessment{}, err
	}
	if err := validateEvidence(catalog, catalogHash, evidence); err != nil {
		return Assessment{}, err
	}
	if err := validateMatrix(matrix, evidence.Binding); err != nil {
		return Assessment{}, err
	}
	return Assessment{
		HarnessStatus: HarnessPass, ReleaseReadiness: ReleaseNoGo,
		EvidenceClass: EvidenceFixtureReplay, ValidatedSuites: RequiredSuites(),
	}, nil
}

func Replay(suite string, catalogRaw, evidenceRaw, matrixRaw []byte) (Assessment, error) {
	if !requiredSuite(suite) {
		return Assessment{}, invalid(CodeSuiteInvalid)
	}
	assessment, err := Validate(catalogRaw, evidenceRaw, matrixRaw)
	if err != nil {
		return Assessment{}, err
	}
	assessment.SelectedSuite = suite
	return assessment, nil
}

func CanonicalCatalogSHA256(raw []byte) (string, error) {
	var catalog Catalog
	if err := decodeDocument(raw, &catalog); err != nil {
		return "", err
	}
	if err := validateCatalog(catalog); err != nil {
		return "", err
	}
	return catalogSHA256(catalog)
}

func catalogSHA256(catalog Catalog) (string, error) {
	normalized := normalizeCatalog(catalog)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", invalid(CodeInvalidJSON)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func validateCatalog(catalog Catalog) error {
	if catalog.SchemaVersion != SchemaVersion {
		return invalid(CodeSchemaVersionInvalid)
	}
	if len(catalog.Suites) != len(requiredSuites) {
		return invalid(CodeSuiteSetInvalid)
	}
	seenSuites := make(map[string]struct{}, len(catalog.Suites))
	for _, suite := range catalog.Suites {
		if !safeID(suite.ID) {
			return invalid(CodeSuiteSetInvalid)
		}
		if _, exists := seenSuites[suite.ID]; exists {
			return invalid(CodeDuplicateID)
		}
		seenSuites[suite.ID] = struct{}{}
		required, exists := requiredCases[suite.ID]
		if !exists {
			return invalid(CodeSuiteSetInvalid)
		}
		if len(suite.Cases) != len(required) {
			return invalid(CodeCaseSetInvalid)
		}
		seenCases := make(map[string]struct{}, len(suite.Cases))
		for _, fixtureCase := range suite.Cases {
			if !safeID(fixtureCase.ID) {
				return invalid(CodeCaseSetInvalid)
			}
			if _, duplicate := seenCases[fixtureCase.ID]; duplicate {
				return invalid(CodeDuplicateID)
			}
			seenCases[fixtureCase.ID] = struct{}{}
			if !contains(required, fixtureCase.ID) {
				return invalid(CodeCaseSetInvalid)
			}
			if err := validateFacts(fixtureCase.ExpectedFacts); err != nil {
				return err
			}
			if !factsEqual(fixtureCase.ExpectedFacts, requiredFacts(fixtureCase.ID)) {
				return invalid(CodeCatalogFactsInvalid)
			}
		}
		for _, caseID := range required {
			if _, exists := seenCases[caseID]; !exists {
				return invalid(CodeCaseSetInvalid)
			}
		}
	}
	for _, suiteID := range requiredSuites {
		if _, exists := seenSuites[suiteID]; !exists {
			return invalid(CodeSuiteSetInvalid)
		}
	}
	return nil
}

func validateEvidence(catalog Catalog, catalogHash string, evidence EvidenceBundle) error {
	if evidence.SchemaVersion != SchemaVersion {
		return invalid(CodeSchemaVersionInvalid)
	}
	if evidence.HarnessStatus != HarnessPass || evidence.ReleaseReadiness != ReleaseNoGo {
		return invalid(CodeReadinessInvalid)
	}
	if evidence.EvidenceClass != EvidenceFixtureReplay {
		return invalid(CodeEvidenceClassInvalid)
	}
	if err := validateBinding(evidence.Binding); err != nil {
		return err
	}
	if evidence.Binding.CatalogSHA256 != catalogHash {
		return invalid(CodeCatalogHashMismatch)
	}
	if err := validateProductionGates(evidence.ProductionGates); err != nil {
		return err
	}
	expected := make(map[string][]Fact)
	for _, suite := range catalog.Suites {
		for _, fixtureCase := range suite.Cases {
			expected[observationKey(suite.ID, fixtureCase.ID)] = fixtureCase.ExpectedFacts
		}
	}
	if len(evidence.Observations) != len(expected) {
		return invalid(CodeObservationSetInvalid)
	}
	seen := make(map[string]struct{}, len(evidence.Observations))
	for _, observation := range evidence.Observations {
		if !safeID(observation.SuiteID) || !safeID(observation.CaseID) {
			return invalid(CodeObservationSetInvalid)
		}
		key := observationKey(observation.SuiteID, observation.CaseID)
		if _, duplicate := seen[key]; duplicate {
			return invalid(CodeDuplicateID)
		}
		seen[key] = struct{}{}
		expectedFacts, exists := expected[key]
		if !exists {
			return invalid(CodeObservationSetInvalid)
		}
		if observation.Binding != evidence.Binding {
			return invalid(CodeBindingMismatch)
		}
		if observation.EvidenceClass != EvidenceFixtureReplay {
			return invalid(CodeEvidenceClassInvalid)
		}
		if observation.VerificationState != VerificationPassed {
			if !validVerificationState(observation.VerificationState) {
				return invalid(CodeVerificationStateInvalid)
			}
			return invalid(CodeObservationInvalid)
		}
		if err := validateFacts(observation.Facts); err != nil {
			return err
		}
		if !factsEqual(observation.Facts, expectedFacts) {
			return invalid(CodeObservationFactsMismatch)
		}
	}
	return nil
}

func validateProductionGates(gates []ProductionGate) error {
	if len(gates) != len(requiredProductionGates) {
		return invalid(CodeProductionGateSetInvalid)
	}
	required := make(map[string]struct{}, len(requiredProductionGates))
	for _, gateID := range requiredProductionGates {
		required[gateID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(gates))
	for _, gate := range gates {
		if !safeID(gate.ID) {
			return invalid(CodeProductionGateSetInvalid)
		}
		if _, duplicate := seen[gate.ID]; duplicate {
			return invalid(CodeDuplicateID)
		}
		seen[gate.ID] = struct{}{}
		if _, exists := required[gate.ID]; !exists {
			return invalid(CodeProductionGateSetInvalid)
		}
		if gate.VerificationState != VerificationNotRun || gate.EvidenceClass != EvidenceSchemaOnly || gate.EvidenceRef != nil {
			return invalid(CodeProductionGateInvalid)
		}
	}
	return nil
}

func validateBinding(binding CandidateBinding) error {
	for _, value := range []string{binding.CandidateCommit, binding.ArtifactSHA256, binding.ConfigSHA256, binding.CatalogSHA256, binding.ToolVersion, binding.CoreVersion, binding.Environment, binding.ObservedAt} {
		if err := validateSafeString(value, maxStringBytes); err != nil {
			return err
		}
	}
	if !lowerHex(binding.CandidateCommit, 40) || !lowerHex(binding.ArtifactSHA256, 64) ||
		!lowerHex(binding.ConfigSHA256, 64) || !lowerHex(binding.CatalogSHA256, 64) ||
		!validVersion(binding.ToolVersion) || !validVersion(binding.CoreVersion) {
		return invalid(CodeBindingInvalid)
	}
	if binding.Environment != EnvironmentOfflineFixture {
		return invalid(CodeOfflineFixtureRequired)
	}
	observed, err := time.Parse(time.RFC3339Nano, binding.ObservedAt)
	if err != nil || observed.Location() != time.UTC || observed.Format(time.RFC3339Nano) != binding.ObservedAt {
		return invalid(CodeTimestampInvalid)
	}
	return nil
}

func validateMatrix(matrix ClientMatrix, expectedBinding CandidateBinding) error {
	if matrix.SchemaVersion != SchemaVersion {
		return invalid(CodeSchemaVersionInvalid)
	}
	if matrix.Binding != expectedBinding {
		return invalid(CodeBindingMismatch)
	}
	if matrix.BaselineVersion != "1.0.157" {
		return invalid(CodeBindingInvalid)
	}
	if len(matrix.Clients) != len(requiredClients) {
		return invalid(CodeClientSetInvalid)
	}
	seenClients := make(map[string]struct{}, len(matrix.Clients))
	for _, client := range matrix.Clients {
		if !contains(requiredClients, client.ID) {
			return invalid(CodeClientSetInvalid)
		}
		if _, duplicate := seenClients[client.ID]; duplicate {
			return invalid(CodeDuplicateID)
		}
		seenClients[client.ID] = struct{}{}
		if err := validateClient(client); err != nil {
			return err
		}
	}
	for _, clientID := range requiredClients {
		if _, exists := seenClients[clientID]; !exists {
			return invalid(CodeClientSetInvalid)
		}
	}
	return nil
}

func validateClient(client ClientRecord) error {
	for _, value := range []*string{client.AppVersion, client.CoreVersion, client.Preset, client.EvidenceRef} {
		if value != nil {
			if err := validateSafeString(*value, maxStringBytes); err != nil {
				return err
			}
		}
	}
	if len(client.Checks) != len(requiredClientChecks) {
		return invalid(CodeClientCheckSetInvalid)
	}
	seenChecks := make(map[string]struct{}, len(client.Checks))
	for _, check := range client.Checks {
		if !contains(requiredClientChecks, check.ID) {
			return invalid(CodeClientCheckSetInvalid)
		}
		if _, duplicate := seenChecks[check.ID]; duplicate {
			return invalid(CodeDuplicateID)
		}
		seenChecks[check.ID] = struct{}{}
		if check.EvidenceRef != nil {
			if err := validateSafeString(*check.EvidenceRef, maxStringBytes); err != nil {
				return err
			}
		}
	}
	if client.VerificationState == VerificationNotRun {
		maestroBaseline := client.ID == "maestrovpn" && client.AppVersion != nil && *client.AppVersion == "1.0.157"
		externalUnversioned := client.ID != "maestrovpn" && client.AppVersion == nil
		if (!maestroBaseline && !externalUnversioned) || client.CoreVersion != nil || client.Preset != nil ||
			client.CompatibilityStatus != nil || client.EvidenceClass != EvidenceSchemaOnly || client.EvidenceRef != nil {
			return invalid(CodeCompatibilityUnobserved)
		}
		for _, check := range client.Checks {
			if check.VerificationState != VerificationNotRun || check.EvidenceClass != EvidenceSchemaOnly || check.EvidenceRef != nil {
				return invalid(CodeCompatibilityUnobserved)
			}
		}
		return nil
	}
	if !validVerificationState(client.VerificationState) || client.CompatibilityStatus == nil ||
		!deviceEvidence(client.EvidenceClass) || client.EvidenceRef == nil || client.CoreVersion == nil || client.Preset == nil || client.AppVersion == nil {
		return invalid(CodeCompatibilityUnobserved)
	}
	if !validCompatibilityStatus(*client.CompatibilityStatus) {
		return invalid(CodeClientEvidenceIncomplete)
	}
	allPassed := true
	importPassed := false
	anyFailed := false
	derivedState := VerificationPassed
	for _, check := range client.Checks {
		if check.VerificationState == VerificationNotRun || !validVerificationState(check.VerificationState) ||
			!deviceEvidence(check.EvidenceClass) || check.EvidenceRef == nil ||
			evidenceRank(check.EvidenceClass) < evidenceRank(client.EvidenceClass) {
			return invalid(CodeClientEvidenceIncomplete)
		}
		if check.VerificationState != VerificationPassed {
			allPassed = false
		}
		if check.VerificationState == VerificationFailed {
			anyFailed = true
			derivedState = VerificationFailed
		} else if check.VerificationState == VerificationBlocked && derivedState != VerificationFailed {
			derivedState = VerificationBlocked
		}
		if check.ID == "import" && check.VerificationState == VerificationPassed {
			importPassed = true
		}
	}
	if client.VerificationState != derivedState {
		return invalid(CodeClientEvidenceIncomplete)
	}
	switch *client.CompatibilityStatus {
	case CompatibilitySupported, CompatibilitySupportedWithSetting:
		if !allPassed || client.VerificationState != VerificationPassed {
			return invalid(CodeClientEvidenceIncomplete)
		}
	case CompatibilityImportOnlyUnstable:
		if !importPassed || allPassed {
			return invalid(CodeClientEvidenceIncomplete)
		}
	case CompatibilityUnsupported:
		if !anyFailed {
			return invalid(CodeClientEvidenceIncomplete)
		}
	}
	return nil
}

func validateFacts(facts []Fact) error {
	if len(facts) == 0 || len(facts) > 16 {
		return invalid(CodeFactInvalid)
	}
	seen := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		if !safeFactName(fact.Name) {
			return invalid(CodeFactInvalid)
		}
		if _, duplicate := seen[fact.Name]; duplicate {
			return invalid(CodeDuplicateID)
		}
		seen[fact.Name] = struct{}{}
		set := 0
		if fact.IntegerValue != nil {
			set++
		}
		if fact.BooleanValue != nil {
			set++
		}
		if fact.TextValue != nil {
			set++
		}
		if set != 1 {
			return invalid(CodeFactInvalid)
		}
		switch fact.Kind {
		case FactInteger:
			if fact.IntegerValue == nil || *fact.IntegerValue < 0 || *fact.IntegerValue > maxFactInteger {
				return invalid(CodeFactInvalid)
			}
		case FactBoolean:
			if fact.BooleanValue == nil {
				return invalid(CodeFactInvalid)
			}
		case FactText:
			if fact.TextValue == nil {
				return invalid(CodeFactInvalid)
			}
			if err := validateSafeString(*fact.TextValue, 128); err != nil {
				return err
			}
		default:
			return invalid(CodeFactInvalid)
		}
	}
	return nil
}

func requiredFacts(caseID string) []Fact {
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
		return []Fact{
			boolean("ledger_unchanged", true),
			integer("next_delta_bytes", 15),
			integer("reset_delta_bytes", 0),
			integer("reset_generation", 2),
			boolean("same_generation_rejected", true),
		}
	case "stable-identity":
		return []Fact{boolean("identity_stable", true)}
	case "idempotent":
		return []Fact{integer("charge_events", 1)}
	case "duplicate-event":
		return []Fact{integer("applied_events", 1)}
	case "out-of-order":
		return []Fact{
			boolean("late_sample_ignored", true),
			boolean("ledger_unchanged", true),
			integer("next_delta_bytes", 10),
		}
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
	case "fixture-inactive-state-render":
		return []Fact{boolean("inactive_state_omitted", true)}
	case "fixture-state-rerender":
		return []Fact{boolean("rerender_reflects_state", true)}
	default:
		return nil
	}
}

func normalizeCatalog(catalog Catalog) Catalog {
	result := Catalog{SchemaVersion: catalog.SchemaVersion, Suites: append([]CatalogSuite(nil), catalog.Suites...)}
	for suiteIndex := range result.Suites {
		result.Suites[suiteIndex].Cases = append([]CatalogCase(nil), result.Suites[suiteIndex].Cases...)
		for caseIndex := range result.Suites[suiteIndex].Cases {
			facts := append([]Fact(nil), result.Suites[suiteIndex].Cases[caseIndex].ExpectedFacts...)
			sort.Slice(facts, func(i, j int) bool { return facts[i].Name < facts[j].Name })
			result.Suites[suiteIndex].Cases[caseIndex].ExpectedFacts = facts
		}
		sort.Slice(result.Suites[suiteIndex].Cases, func(i, j int) bool {
			return result.Suites[suiteIndex].Cases[i].ID < result.Suites[suiteIndex].Cases[j].ID
		})
	}
	sort.Slice(result.Suites, func(i, j int) bool { return result.Suites[i].ID < result.Suites[j].ID })
	return result
}

func factsEqual(left, right []Fact) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]Fact(nil), left...)
	rightCopy := append([]Fact(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i].Name < leftCopy[j].Name })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i].Name < rightCopy[j].Name })
	for index := range leftCopy {
		if !factEqual(leftCopy[index], rightCopy[index]) {
			return false
		}
	}
	return true
}

func factEqual(left, right Fact) bool {
	if left.Name != right.Name || left.Kind != right.Kind {
		return false
	}
	if (left.IntegerValue == nil) != (right.IntegerValue == nil) || (left.BooleanValue == nil) != (right.BooleanValue == nil) || (left.TextValue == nil) != (right.TextValue == nil) {
		return false
	}
	if left.IntegerValue != nil && *left.IntegerValue != *right.IntegerValue {
		return false
	}
	if left.BooleanValue != nil && *left.BooleanValue != *right.BooleanValue {
		return false
	}
	return left.TextValue == nil || *left.TextValue == *right.TextValue
}

var duplicateKeyError = errors.New("duplicate json key")

func decodeDocument(raw []byte, destination any) error {
	if len(raw) > MaxDocumentBytes {
		return invalid(CodeDocumentTooLarge)
	}
	if len(raw) == 0 {
		return invalid(CodeInvalidJSON)
	}
	if !utf8.Valid(raw) {
		return invalid(CodeInvalidUTF8)
	}
	if err := scanJSON(raw); err != nil {
		if errors.Is(err, duplicateKeyError) {
			return invalid(CodeDuplicateJSONKey)
		}
		return invalid(CodeInvalidJSON)
	}
	if !validExactJSONKeys(raw, destination) {
		return invalid(CodeUnknownField)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return invalid(CodeUnknownField)
		}
		return invalid(CodeInvalidJSON)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return invalid(CodeInvalidJSON)
	}
	return nil
}

func validExactJSONKeys(raw []byte, destination any) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	switch destination.(type) {
	case *Catalog:
		return validCatalogJSONShape(value)
	case *EvidenceBundle:
		return validEvidenceJSONShape(value)
	case *ClientMatrix:
		return validMatrixJSONShape(value)
	default:
		return false
	}
}

func validCatalogJSONShape(value any) bool {
	object, ok := exactObject(value, "schema_version", "suites")
	if !ok {
		return false
	}
	suites, ok := object["suites"].([]any)
	if !ok {
		return false
	}
	for _, suiteValue := range suites {
		suite, ok := exactObject(suiteValue, "id", "cases")
		if !ok {
			return false
		}
		cases, ok := suite["cases"].([]any)
		if !ok {
			return false
		}
		for _, caseValue := range cases {
			fixtureCase, ok := exactObject(caseValue, "id", "expected_facts")
			if !ok || !validFactsJSONShape(fixtureCase["expected_facts"]) {
				return false
			}
		}
	}
	return true
}

func validEvidenceJSONShape(value any) bool {
	object, ok := exactObject(value, "schema_version", "binding", "evidence_class", "harness_status", "release_readiness", "production_gates", "observations")
	if !ok || !validBindingJSONShape(object["binding"]) {
		return false
	}
	gates, ok := object["production_gates"].([]any)
	if !ok {
		return false
	}
	for _, gateValue := range gates {
		if _, ok := exactObject(gateValue, "id", "verification_state", "evidence_class", "evidence_ref"); !ok {
			return false
		}
	}
	observations, ok := object["observations"].([]any)
	if !ok {
		return false
	}
	for _, observationValue := range observations {
		observation, ok := exactObject(observationValue, "suite_id", "case_id", "binding", "evidence_class", "verification_state", "facts")
		if !ok || !validBindingJSONShape(observation["binding"]) || !validFactsJSONShape(observation["facts"]) {
			return false
		}
	}
	return true
}

func validMatrixJSONShape(value any) bool {
	object, ok := exactObject(value, "binding", "schema_version", "baseline_version", "clients")
	if !ok || !validBindingJSONShape(object["binding"]) {
		return false
	}
	clients, ok := object["clients"].([]any)
	if !ok {
		return false
	}
	for _, clientValue := range clients {
		client, ok := exactObject(clientValue, "id", "app_version", "core_version", "preset", "verification_state", "compatibility_status", "evidence_class", "evidence_ref", "checks")
		if !ok {
			return false
		}
		checks, ok := client["checks"].([]any)
		if !ok {
			return false
		}
		for _, checkValue := range checks {
			if _, ok := exactObject(checkValue, "id", "verification_state", "evidence_class", "evidence_ref"); !ok {
				return false
			}
		}
	}
	return true
}

func validBindingJSONShape(value any) bool {
	_, ok := exactObject(value, "candidate_commit", "artifact_sha256", "config_sha256", "catalog_sha256", "tool_version", "core_version", "environment", "observed_at")
	return ok
}

func validFactsJSONShape(value any) bool {
	facts, ok := value.([]any)
	if !ok {
		return false
	}
	for _, fact := range facts {
		if _, ok := exactObject(fact, "name", "kind", "integer_value", "boolean_value", "text_value"); !ok {
			return false
		}
	}
	return true
}

func exactObject(value any, keys ...string) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != len(keys) {
		return nil, false
	}
	for _, key := range keys {
		if _, exists := object[key]; !exists {
			return nil, false
		}
	}
	return object, true
}

func scanJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return errors.New("json depth")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return duplicateKeyError
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("object close")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("array close")
		}
	default:
		return errors.New("unexpected delimiter")
	}
	return nil
}

func validateSafeString(value string, max int) error {
	if value == "" || len(value) > max || !utf8.ValidString(value) || value != strings.TrimSpace(value) {
		return invalid(CodeUnsafeString)
	}
	for _, current := range value {
		if current < 0x20 || current == 0x7f {
			return invalid(CodeUnsafeString)
		}
	}
	lower := strings.ToLower(value)
	for _, fragment := range []string{"://", "-----begin", "/sub/", "token=", "password=", "secret=", "private_key"} {
		if strings.Contains(lower, fragment) {
			return invalid(CodeUnsafeString)
		}
	}
	return nil
}

func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, current := range value {
		if (current < '0' || current > '9') && (current < 'a' || current > 'f') {
			return false
		}
	}
	return true
}

func validVersion(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index, current := range value {
		if (current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z') || (current >= '0' && current <= '9') {
			continue
		}
		if index > 0 && (current == '.' || current == '_' || current == '+' || current == '-') {
			continue
		}
		return false
	}
	return true
}

func safeID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, current := range value {
		if (current >= 'a' && current <= 'z') || (current >= '0' && current <= '9') || current == '_' || current == '-' {
			continue
		}
		return false
	}
	return true
}

func safeFactName(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, current := range value {
		if (current >= 'a' && current <= 'z') || (current >= '0' && current <= '9') || current == '_' {
			continue
		}
		return false
	}
	return true
}

func requiredSuite(value string) bool { return contains(requiredSuites, value) }

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func observationKey(suite, fixtureCase string) string { return suite + "\x00" + fixtureCase }

func validVerificationState(value VerificationState) bool {
	return value == VerificationNotRun || value == VerificationPassed || value == VerificationFailed || value == VerificationBlocked
}

func deviceEvidence(value EvidenceClass) bool {
	return value == EvidenceDeviceObserved || value == EvidenceProductionObserved
}

func evidenceRank(value EvidenceClass) int {
	switch value {
	case EvidenceSchemaOnly:
		return 1
	case EvidenceFixtureReplay:
		return 2
	case EvidenceIsolatedRealBinary:
		return 3
	case EvidenceDeviceObserved:
		return 4
	case EvidenceProductionObserved:
		return 5
	default:
		return 0
	}
}

func validCompatibilityStatus(value CompatibilityStatus) bool {
	return value == CompatibilitySupported || value == CompatibilitySupportedWithSetting || value == CompatibilityExperimental || value == CompatibilityImportOnlyUnstable || value == CompatibilityUnsupported
}
