package whitelistready

import "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"

const (
	SchemaVersion    = 1
	MaxDocumentBytes = 512 * 1024

	HarnessPass               = "PASS"
	ReleaseNoGo               = "NO_GO"
	EnvironmentOfflineFixture = "OFFLINE_FIXTURE"
)

type EvidenceClass = release.EvidenceClass

const (
	EvidenceSchemaOnly         = release.EvidenceSchemaOnly
	EvidenceFixtureReplay      = release.EvidenceFixtureReplay
	EvidenceIsolatedRealBinary = release.EvidenceIsolatedRealBinary
	EvidenceDeviceObserved     = release.EvidenceDeviceObserved
	EvidenceProductionObserved = release.EvidenceProductionObserved
)

type VerificationState string

const (
	VerificationNotRun  VerificationState = "NOT_RUN"
	VerificationPassed  VerificationState = "PASSED"
	VerificationFailed  VerificationState = "FAILED"
	VerificationBlocked VerificationState = "BLOCKED"
)

type CompatibilityStatus string

const (
	CompatibilitySupported            CompatibilityStatus = "SUPPORTED"
	CompatibilitySupportedWithSetting CompatibilityStatus = "SUPPORTED_WITH_SETTING"
	CompatibilityExperimental         CompatibilityStatus = "EXPERIMENTAL"
	CompatibilityImportOnlyUnstable   CompatibilityStatus = "IMPORT_ONLY_UNSTABLE"
	CompatibilityUnsupported          CompatibilityStatus = "UNSUPPORTED"
)

type FactKind string

const (
	FactInteger FactKind = "INTEGER"
	FactBoolean FactKind = "BOOLEAN"
	FactText    FactKind = "TEXT"
)

const (
	CodeDocumentTooLarge         = "document_too_large"
	CodeInvalidUTF8              = "invalid_utf8"
	CodeInvalidJSON              = "invalid_json"
	CodeDuplicateJSONKey         = "duplicate_json_key"
	CodeUnknownField             = "unknown_field"
	CodeSchemaVersionInvalid     = "schema_version_invalid"
	CodeSuiteSetInvalid          = "suite_set_invalid"
	CodeSuiteInvalid             = "suite_invalid"
	CodeCaseSetInvalid           = "case_set_invalid"
	CodeDuplicateID              = "duplicate_id"
	CodeCatalogFactsInvalid      = "catalog_facts_invalid"
	CodeCatalogHashMismatch      = "catalog_hash_mismatch"
	CodeBindingInvalid           = "binding_invalid"
	CodeBindingMismatch          = "binding_mismatch"
	CodeTimestampInvalid         = "timestamp_invalid"
	CodeOfflineFixtureRequired   = "offline_fixture_required"
	CodeEvidenceClassInvalid     = "evidence_class_invalid"
	CodeVerificationStateInvalid = "verification_state_invalid"
	CodeObservationInvalid       = "observation_invalid"
	CodeObservationSetInvalid    = "observation_set_invalid"
	CodeObservationFactsMismatch = "observation_facts_mismatch"
	CodeFactInvalid              = "fact_invalid"
	CodeUnsafeString             = "unsafe_string"
	CodeReadinessInvalid         = "readiness_invalid"
	CodeClientSetInvalid         = "client_set_invalid"
	CodeClientCheckSetInvalid    = "client_check_set_invalid"
	CodeCompatibilityUnobserved  = "compatibility_unobserved"
	CodeClientEvidenceIncomplete = "client_evidence_incomplete"
)

type Fact struct {
	Name         string   `json:"name"`
	Kind         FactKind `json:"kind"`
	IntegerValue *int64   `json:"integer_value"`
	BooleanValue *bool    `json:"boolean_value"`
	TextValue    *string  `json:"text_value"`
}

type CatalogCase struct {
	ID            string `json:"id"`
	ExpectedFacts []Fact `json:"expected_facts"`
}

type CatalogSuite struct {
	ID    string        `json:"id"`
	Cases []CatalogCase `json:"cases"`
}

type Catalog struct {
	SchemaVersion int            `json:"schema_version"`
	Suites        []CatalogSuite `json:"suites"`
}

type CandidateBinding struct {
	CandidateCommit string `json:"candidate_commit"`
	ArtifactSHA256  string `json:"artifact_sha256"`
	ConfigSHA256    string `json:"config_sha256"`
	CatalogSHA256   string `json:"catalog_sha256"`
	ToolVersion     string `json:"tool_version"`
	CoreVersion     string `json:"core_version"`
	Environment     string `json:"environment"`
	ObservedAt      string `json:"observed_at"`
}

type Observation struct {
	SuiteID           string            `json:"suite_id"`
	CaseID            string            `json:"case_id"`
	Binding           CandidateBinding  `json:"binding"`
	EvidenceClass     EvidenceClass     `json:"evidence_class"`
	VerificationState VerificationState `json:"verification_state"`
	Facts             []Fact            `json:"facts"`
}

type EvidenceBundle struct {
	SchemaVersion    int              `json:"schema_version"`
	Binding          CandidateBinding `json:"binding"`
	EvidenceClass    EvidenceClass    `json:"evidence_class"`
	HarnessStatus    string           `json:"harness_status"`
	ReleaseReadiness string           `json:"release_readiness"`
	Observations     []Observation    `json:"observations"`
}

type ClientCheck struct {
	ID                string            `json:"id"`
	VerificationState VerificationState `json:"verification_state"`
	EvidenceClass     EvidenceClass     `json:"evidence_class"`
	EvidenceRef       *string           `json:"evidence_ref"`
}

type ClientRecord struct {
	ID                  string               `json:"id"`
	AppVersion          *string              `json:"app_version"`
	CoreVersion         *string              `json:"core_version"`
	Preset              *string              `json:"preset"`
	VerificationState   VerificationState    `json:"verification_state"`
	CompatibilityStatus *CompatibilityStatus `json:"compatibility_status"`
	EvidenceClass       EvidenceClass        `json:"evidence_class"`
	EvidenceRef         *string              `json:"evidence_ref"`
	Checks              []ClientCheck        `json:"checks"`
}

type ClientMatrix struct {
	Binding         CandidateBinding `json:"binding"`
	SchemaVersion   int              `json:"schema_version"`
	BaselineVersion string           `json:"baseline_version"`
	Clients         []ClientRecord   `json:"clients"`
}

type Assessment struct {
	HarnessStatus    string        `json:"harness_status"`
	ReleaseReadiness string        `json:"release_readiness"`
	EvidenceClass    EvidenceClass `json:"evidence_class"`
	ValidatedSuites  []string      `json:"validated_suites,omitempty"`
	SelectedSuite    string        `json:"selected_suite,omitempty"`
}

var requiredSuites = []string{
	"yandex_get_body",
	"yandex_active_stream",
	"yandex_idle_cutoff",
	"yandex_literal_edge",
	"xray_counter_reset",
	"billing_idempotency",
	"duplicate_event_replay",
	"subscription_escaping",
	"edge_rotation",
}

var requiredCases = map[string][]string{
	"yandex_get_body": {
		"body-1b", "body-1kib", "body-64kib", "body-256kib", "body-typical", "body-max",
		"auth-pass", "sequence-pass", "cache-disabled", "invalid-host-rejected", "invalid-path-rejected",
		"invalid-status-rejected", "latency-bounded", "retry-bounded",
	},
	"yandex_active_stream":   {"active-stream-5m"},
	"yandex_idle_cutoff":     {"idle-90s-recovery"},
	"yandex_literal_edge":    {"literal-edge-get"},
	"xray_counter_reset":     {"counter-reset"},
	"billing_idempotency":    {"stable-identity", "idempotent"},
	"duplicate_event_replay": {"duplicate-event", "out-of-order"},
	"subscription_escaping":  {"base64", "plain", "utf8", "escaping", "long-uri", "qr", "refresh", "dedup", "reimport", "revocation", "cache-invalidation"},
	"edge_rotation":          {"primary", "failover", "finite-fallback"},
}

var requiredClients = []string{"maestrovpn", "karing", "incy", "happ"}

var requiredClientChecks = []string{
	"import", "refresh", "tls", "client_encryption", "xhttp_get", "tcp", "udp", "dns", "speedtest",
	"five_min_up_down", "idle_90s_recovery", "network_transitions", "sleep_wake", "cold_start",
	"literal_edge", "per_user_stats", "billing_identity",
}

func RequiredSuites() []string { return append([]string(nil), requiredSuites...) }

func RequiredCaseIDs(suite string) []string { return append([]string(nil), requiredCases[suite]...) }

func RequiredClients() []string { return append([]string(nil), requiredClients...) }

func RequiredClientChecks() []string { return append([]string(nil), requiredClientChecks...) }
