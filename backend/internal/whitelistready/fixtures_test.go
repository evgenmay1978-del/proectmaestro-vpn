package whitelistready

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const fixtureGenerationEnv = "MAESTRO_GENERATE_TASK7_FIXTURES"

func TestRepositoryFixturesValidate(t *testing.T) {
	catalog := readRepositoryFixture(t, "acceptance-catalog.v1.json")
	evidence := readRepositoryFixture(t, "acceptance-evidence.v1.json")
	matrix := readRepositoryFixture(t, "client-compatibility-matrix.v1.json")
	assessment, err := Validate(catalog, evidence, matrix)
	if err != nil {
		t.Fatalf("repository fixtures failed validation: %v", err)
	}
	if assessment.ReleaseReadiness != ReleaseNoGo || assessment.EvidenceClass != EvidenceFixtureReplay {
		t.Fatalf("repository fixtures upgraded readiness: %#v", assessment)
	}
	for _, suite := range RequiredSuites() {
		replay, err := Replay(suite, catalog, evidence, matrix)
		if err != nil || replay.SelectedSuite != suite || replay.ReleaseReadiness != ReleaseNoGo {
			t.Fatalf("suite %s replay=%#v err=%v", suite, replay, err)
		}
	}
}

func TestGenerateRepositoryFixtures(t *testing.T) {
	if os.Getenv(fixtureGenerationEnv) != "1" {
		t.Skip("fixture generation is explicitly gated")
	}
	catalog, evidence, matrix := canonicalFixtureModels(t)
	writeRepositoryFixture(t, "acceptance-catalog.v1.json", catalog)
	writeRepositoryFixture(t, "acceptance-evidence.v1.json", evidence)
	writeRepositoryFixture(t, "client-compatibility-matrix.v1.json", matrix)
}

func canonicalFixtureModels(t *testing.T) (Catalog, EvidenceBundle, ClientMatrix) {
	t.Helper()
	catalog := Catalog{SchemaVersion: SchemaVersion}
	for _, suiteID := range RequiredSuites() {
		suite := CatalogSuite{ID: suiteID}
		for _, caseID := range RequiredCaseIDs(suiteID) {
			suite.Cases = append(suite.Cases, CatalogCase{ID: caseID, ExpectedFacts: requiredFacts(caseID)})
		}
		catalog.Suites = append(catalog.Suites, suite)
	}
	catalogHash, err := catalogSHA256(catalog)
	if err != nil {
		t.Fatal(err)
	}
	binding := CandidateBinding{
		CandidateCommit: "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ConfigSHA256:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CatalogSHA256:   catalogHash,
		ToolVersion:     "task7-harness-1.0.0",
		CoreVersion:     "xray-fixture-25.8.3",
		Environment:     EnvironmentOfflineFixture,
		ObservedAt:      "2026-08-22T00:00:00Z",
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
				Facts:             fixtureCase.ExpectedFacts,
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

func fixtureDirectory(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "scripts", "repro", "fixtures")
}

func readRepositoryFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDirectory(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeRepositoryFixture(t *testing.T, name string, value any) {
	t.Helper()
	directory := fixtureDirectory(t)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(directory, name), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
