package release

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const (
	SchemaVersion     = 1
	SidecarPort       = 18081
	StatsAPIPort      = 18082
	FallbackProbePort = 18080
	RuntimeConfigPath = "/run/maestro-xray-cdn/config.json"
	maxManifestBytes  = 64 << 10
	maxBinaryBytes    = 256 << 20
)

var (
	ErrInvalidRelease = errors.New("release: invalid isolated release")
	safeIDPattern     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)
	versionPattern    = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
	requiredGates     = []string{
		"billing_identity", "client_import", "config_validation", "direct_origin",
		"isolated_start", "literal_edge", "local_vless", "per_user_stats",
		"production_baseline", "subscription_regression", "xray_config_test", "yandex_get_body",
	}
)

type ValidationError struct{ code string }

func (e ValidationError) Error() string      { return "release validation failed: " + e.code }
func (e ValidationError) ReasonCode() string { return e.code }
func (e ValidationError) Unwrap() error      { return ErrInvalidRelease }
func invalid(code string) error              { return ValidationError{code: code} }

type State string

const (
	Draft     State = "DRAFT"
	Candidate State = "CANDIDATE"
	Published State = "PUBLISHED"
	Retired   State = "RETIRED"
)

type Artifact struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	SchemaVersion         int        `json:"schema_version"`
	ReleaseID             string     `json:"release_id"`
	Generation            uint64     `json:"generation"`
	TransportProfileID    string     `json:"transport_profile_id"`
	CompatibilityPresetID string     `json:"compatibility_preset_id"`
	XrayVersion           string     `json:"xray_version"`
	XrayCommit            string     `json:"xray_commit"`
	XraySource            string     `json:"xray_source"`
	XrayBinarySHA256      string     `json:"xray_binary_sha256"`
	CandidateSHA256       string     `json:"candidate_sha256"`
	RuntimeConfigPath     string     `json:"runtime_config_path"`
	TargetPort            int        `json:"target_port"`
	FallbackProbePort     int        `json:"fallback_probe_port"`
	Artifacts             []Artifact `json:"artifacts"`
}

type ValidationEvidence struct {
	SchemaVersion   int               `json:"schema_version"`
	CandidateSHA256 string            `json:"candidate_sha256"`
	Gates           map[string]string `json:"gates"`
}

type CandidateSpec struct {
	Transport          controlplane.TransportRelease
	Generation         uint64
	XrayVersion        string
	XrayCommit         string
	XraySource         string
	XrayBinarySHA256   string
	ValidationEvidence ValidationEvidence
	XrayBinary         []byte
	ConfigJSON         []byte
	SystemdUnit        []byte
	RollbackJSON       []byte
}

type Release struct {
	manifest       Manifest
	canonical      []byte
	manifestSHA256 string
	state          State
	transport      controlplane.TransportRelease
	configTemplate []byte
}

type candidateBinding struct {
	SchemaVersion         int    `json:"schema_version"`
	ReleaseID             string `json:"release_id"`
	Generation            uint64 `json:"generation"`
	TransportProfileID    string `json:"transport_profile_id"`
	CompatibilityPresetID string `json:"compatibility_preset_id"`
	XrayVersion           string `json:"xray_version"`
	XrayCommit            string `json:"xray_commit"`
	XraySource            string `json:"xray_source"`
	XrayBinarySHA256      string `json:"xray_binary_sha256"`
	ConfigSHA256          string `json:"config_sha256"`
	SystemdSHA256         string `json:"systemd_sha256"`
	RollbackSHA256        string `json:"rollback_sha256"`
}

func NewCandidate(spec CandidateSpec) (Release, error) {
	if err := validateCandidateInputs(spec); err != nil {
		return Release{}, err
	}
	candidateSHA, err := candidateDigest(spec)
	if err != nil || subtle.ConstantTimeCompare([]byte(candidateSHA), []byte(spec.ValidationEvidence.CandidateSHA256)) != 1 {
		return Release{}, invalid("evidence_candidate_mismatch")
	}
	if err := validateEvidence(spec.ValidationEvidence); err != nil {
		return Release{}, err
	}
	evidenceJSON, err := json.Marshal(spec.ValidationEvidence)
	if err != nil {
		return Release{}, invalid("evidence_encode")
	}
	artifacts := artifactManifest(map[string][]byte{
		"config.json":              spec.ConfigJSON,
		"maestro-xray-cdn.service": spec.SystemdUnit,
		"rollback.json":            spec.RollbackJSON,
		"validation-report.json":   evidenceJSON,
		"xray":                     spec.XrayBinary,
	})
	manifest := Manifest{
		SchemaVersion: SchemaVersion, ReleaseID: spec.Transport.ID(), Generation: spec.Generation,
		TransportProfileID: spec.Transport.TransportProfileID(), CompatibilityPresetID: spec.Transport.CompatibilityPresetID(),
		XrayVersion: spec.XrayVersion, XrayCommit: spec.XrayCommit, XraySource: spec.XraySource,
		XrayBinarySHA256: spec.XrayBinarySHA256, CandidateSHA256: candidateSHA, RuntimeConfigPath: RuntimeConfigPath,
		TargetPort: SidecarPort, FallbackProbePort: FallbackProbePort, Artifacts: artifacts,
	}
	if err := validateManifest(manifest); err != nil {
		return Release{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || len(canonical) > maxManifestBytes {
		return Release{}, invalid("manifest_encode")
	}
	return Release{
		manifest: cloneManifest(manifest), canonical: append([]byte(nil), canonical...),
		manifestSHA256: digestBytes(canonical), state: Candidate, transport: spec.Transport,
		configTemplate: append([]byte(nil), spec.ConfigJSON...),
	}, nil
}

func BuildValidationEvidence(spec CandidateSpec, gates map[string]string) (ValidationEvidence, error) {
	digest, err := candidateDigest(spec)
	if err != nil {
		return ValidationEvidence{}, err
	}
	evidence := ValidationEvidence{SchemaVersion: 1, CandidateSHA256: digest, Gates: cloneMap(gates)}
	if err := validateEvidence(evidence); err != nil {
		return ValidationEvidence{}, err
	}
	return evidence, nil
}

func RequiredValidationGates() []string { return append([]string(nil), requiredGates...) }

func validateCandidateInputs(spec CandidateSpec) error {
	if spec.Transport.State() != controlplane.TransportReleaseCandidate || spec.Generation == 0 ||
		!validID(spec.Transport.ID()) || !validID(spec.Transport.TransportProfileID()) ||
		!validID(spec.Transport.CompatibilityPresetID()) || !versionPattern.MatchString(spec.XrayVersion) ||
		!validCommit(spec.XrayCommit) || !validPinnedSource(spec.XraySource) || len(spec.XrayBinary) < 4 ||
		len(spec.XrayBinary) > maxBinaryBytes || !bytes.Equal(spec.XrayBinary[:4], []byte{0x7f, 'E', 'L', 'F'}) ||
		ValidateConfigTemplate(spec.ConfigJSON) != nil || ValidateSystemdTemplate(spec.SystemdUnit) != nil ||
		ValidateRollbackTemplate(spec.RollbackJSON) != nil {
		return invalid("candidate_input_invalid")
	}
	if actual := digestBytes(spec.XrayBinary); !validSHA256(spec.XrayBinarySHA256) || subtle.ConstantTimeCompare([]byte(spec.XrayBinarySHA256), []byte(actual)) != 1 {
		return invalid("xray_binary_digest_mismatch")
	}
	return nil
}

func candidateDigest(spec CandidateSpec) (string, error) {
	if err := validateCandidateInputs(spec); err != nil {
		return "", err
	}
	binding := candidateBinding{
		SchemaVersion: SchemaVersion, ReleaseID: spec.Transport.ID(), Generation: spec.Generation,
		TransportProfileID: spec.Transport.TransportProfileID(), CompatibilityPresetID: spec.Transport.CompatibilityPresetID(),
		XrayVersion: spec.XrayVersion, XrayCommit: spec.XrayCommit, XraySource: spec.XraySource,
		XrayBinarySHA256: spec.XrayBinarySHA256, ConfigSHA256: digestBytes(spec.ConfigJSON),
		SystemdSHA256: digestBytes(spec.SystemdUnit), RollbackSHA256: digestBytes(spec.RollbackJSON),
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return "", invalid("candidate_binding_encode")
	}
	return digestBytes(raw), nil
}

func validateEvidence(evidence ValidationEvidence) error {
	if evidence.SchemaVersion != 1 || !validSHA256(evidence.CandidateSHA256) || len(evidence.Gates) != len(requiredGates) {
		return invalid("validation_evidence_invalid")
	}
	for _, gate := range requiredGates {
		if !validSHA256(evidence.Gates[gate]) {
			return invalid("validation_gate_missing")
		}
	}
	return nil
}

func ParseManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > maxManifestBytes || !utf8.Valid(raw) {
		return Manifest{}, invalid("manifest_bytes_invalid")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, invalid("manifest_json_invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, invalid("manifest_trailing_data")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Manifest{}, invalid("manifest_not_canonical")
	}
	return cloneManifest(manifest), nil
}

func (r Release) Manifest() Manifest                       { return cloneManifest(r.manifest) }
func (r Release) CanonicalManifest() []byte                { return append([]byte(nil), r.canonical...) }
func (r Release) ManifestSHA256() string                   { return r.manifestSHA256 }
func (r Release) State() State                             { return r.state }
func (r Release) Transport() controlplane.TransportRelease { return r.transport }
func (r Release) MaterializeRuntimeConfig(material map[string]string) ([]byte, error) {
	return materializeRuntimeConfig(r.configTemplate, material)
}

func (r Release) VerifyArtifacts(artifacts map[string][]byte) error {
	if len(artifacts) != len(r.manifest.Artifacts) {
		return invalid("artifact_set_mismatch")
	}
	for _, artifact := range r.manifest.Artifacts {
		data, ok := artifacts[artifact.Path]
		if !ok || int64(len(data)) != artifact.Size || subtle.ConstantTimeCompare([]byte(digestBytes(data)), []byte(artifact.SHA256)) != 1 {
			return invalid("artifact_digest_mismatch")
		}
		if err := validateArtifactContent(artifact.Path, data); err != nil {
			return err
		}
		switch artifact.Path {
		case "validation-report.json":
			var evidence ValidationEvidence
			if err := decodeCanonicalJSON(data, &evidence); err != nil ||
				subtle.ConstantTimeCompare([]byte(evidence.CandidateSHA256), []byte(r.manifest.CandidateSHA256)) != 1 {
				return invalid("evidence_manifest_mismatch")
			}
		case "xray":
			if subtle.ConstantTimeCompare([]byte(artifact.SHA256), []byte(r.manifest.XrayBinarySHA256)) != 1 {
				return invalid("xray_manifest_mismatch")
			}
		}
	}
	return nil
}

func artifactManifest(values map[string][]byte) []Artifact {
	paths := make([]string, 0, len(values))
	for path := range values {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	artifacts := make([]Artifact, 0, len(paths))
	for _, path := range paths {
		artifacts = append(artifacts, Artifact{Path: path, Size: int64(len(values[path])), SHA256: digestBytes(values[path])})
	}
	return artifacts
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion || manifest.Generation == 0 || !validID(manifest.ReleaseID) ||
		!validID(manifest.TransportProfileID) || !validID(manifest.CompatibilityPresetID) ||
		!versionPattern.MatchString(manifest.XrayVersion) || !validCommit(manifest.XrayCommit) ||
		!validPinnedSource(manifest.XraySource) || !validSHA256(manifest.XrayBinarySHA256) ||
		!validSHA256(manifest.CandidateSHA256) || manifest.RuntimeConfigPath != RuntimeConfigPath ||
		manifest.TargetPort != SidecarPort || manifest.FallbackProbePort != FallbackProbePort ||
		len(manifest.Artifacts) != len(allowedArtifactPaths()) {
		return invalid("manifest_fields_invalid")
	}
	allowed := allowedArtifactPaths()
	for index, artifact := range manifest.Artifacts {
		if artifact.Path != allowed[index] || artifact.Size <= 0 || artifact.Size > artifactSizeLimit(artifact.Path) || !validSHA256(artifact.SHA256) {
			return invalid("manifest_artifact_invalid")
		}
	}
	return nil
}

func allowedArtifactPaths() []string {
	return []string{"config.json", "maestro-xray-cdn.service", "rollback.json", "validation-report.json", "xray"}
}

func artifactSizeLimit(path string) int64 {
	switch path {
	case "config.json":
		return 1 << 20
	case "maestro-xray-cdn.service", "rollback.json", "validation-report.json":
		return 64 << 10
	case "xray":
		return maxBinaryBytes
	default:
		return 0
	}
}

func validateArtifactContent(path string, data []byte) error {
	switch path {
	case "config.json":
		return ValidateConfigTemplate(data)
	case "maestro-xray-cdn.service":
		return ValidateSystemdTemplate(data)
	case "rollback.json":
		return ValidateRollbackTemplate(data)
	case "validation-report.json":
		var evidence ValidationEvidence
		if err := decodeCanonicalJSON(data, &evidence); err != nil {
			return err
		}
		return validateEvidence(evidence)
	case "xray":
		if len(data) < 4 || len(data) > maxBinaryBytes || !bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) {
			return invalid("xray_binary_invalid")
		}
		return nil
	default:
		return invalid("artifact_path_invalid")
	}
}

func cloneManifest(manifest Manifest) Manifest {
	copy := manifest
	copy.Artifacts = append([]Artifact(nil), manifest.Artifacts...)
	return copy
}

func cloneMap(values map[string]string) map[string]string {
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func validID(value string) bool {
	return value == strings.TrimSpace(value) && !strings.Contains(value, "..") && safeIDPattern.MatchString(value)
}

func validCommit(value string) bool {
	decoded, err := hex.DecodeString(value)
	return len(value) == 40 && value == strings.ToLower(value) && err == nil && len(decoded) == 20
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return len(value) == 64 && value == strings.ToLower(value) && err == nil && len(decoded) == sha256.Size
}

func validPinnedSource(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		parsed.RawQuery == "" && parsed.Fragment == "" && !strings.HasSuffix(parsed.Path, "/latest")
}

func (r Release) withState(state State) (Release, error) {
	allowed := r.state == Draft && state == Candidate || r.state == Candidate && state == Published ||
		r.state == Published && state == Retired || r.state == Retired && state == Published
	if !allowed {
		return Release{}, invalid("lifecycle_transition_invalid")
	}
	return r.setState(state)
}

func (r Release) setState(state State) (Release, error) {
	transportState := controlplane.TransportReleaseCandidate
	switch state {
	case Draft:
		transportState = controlplane.TransportReleaseDraft
	case Candidate:
		transportState = controlplane.TransportReleaseCandidate
	case Published:
		transportState = controlplane.TransportReleasePublished
	case Retired:
		transportState = controlplane.TransportReleaseRetired
	default:
		return Release{}, invalid("lifecycle_state_invalid")
	}
	transport, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID: r.transport.ID(), Profile: r.transport.Profile(), Preset: r.transport.Preset(),
		State: transportState, ApprovedEdges: r.transport.ApprovedEdges(),
	})
	if err != nil {
		return Release{}, invalid("transport_state_invalid")
	}
	copy := cloneRelease(r)
	copy.state = state
	copy.transport = transport
	return copy, nil
}

func cloneRelease(r Release) Release {
	copy := r
	copy.manifest = cloneManifest(r.manifest)
	copy.canonical = append([]byte(nil), r.canonical...)
	copy.configTemplate = append([]byte(nil), r.configTemplate...)
	return copy
}

type Catalog struct{ releases map[string]Release }

func NewCatalog() Catalog { return Catalog{releases: make(map[string]Release)} }

func (c Catalog) AddDraft(spec CandidateSpec) (Catalog, error) {
	candidate, err := NewCandidate(spec)
	if err != nil {
		return Catalog{}, err
	}
	draft, err := candidate.setState(Draft)
	if err != nil {
		return Catalog{}, err
	}
	return c.add(draft)
}

func (c Catalog) PromoteDraft(releaseID string) (Catalog, error) {
	draft, exists := c.releases[releaseID]
	if !exists || draft.state != Draft {
		return Catalog{}, invalid("draft_not_found")
	}
	candidate, err := draft.withState(Candidate)
	if err != nil {
		return Catalog{}, err
	}
	copy := c.clone()
	copy.releases[releaseID] = candidate
	return copy, nil
}

func (c Catalog) AddCandidate(candidate Release) (Catalog, error) {
	if candidate.state != Candidate {
		return Catalog{}, invalid("candidate_state_invalid")
	}
	return c.add(candidate)
}

func (c Catalog) add(value Release) (Catalog, error) {
	if !validID(value.manifest.ReleaseID) {
		return Catalog{}, invalid("release_id_invalid")
	}
	copy := c.clone()
	if _, exists := copy.releases[value.manifest.ReleaseID]; exists {
		return Catalog{}, invalid("release_duplicate")
	}
	for _, existing := range copy.releases {
		if existing.manifest.TransportProfileID == value.manifest.TransportProfileID && existing.manifest.Generation == value.manifest.Generation {
			return Catalog{}, invalid("generation_duplicate")
		}
		if existing.manifestSHA256 == value.manifestSHA256 {
			return Catalog{}, invalid("manifest_duplicate")
		}
	}
	copy.releases[value.manifest.ReleaseID] = cloneRelease(value)
	return copy, nil
}

func (c Catalog) Publish(releaseID string) (Catalog, error) {
	candidate, exists := c.releases[releaseID]
	if !exists || candidate.state != Candidate {
		return Catalog{}, invalid("candidate_not_found")
	}
	profileID := candidate.manifest.TransportProfileID
	for _, existing := range c.releases {
		if existing.state == Published && existing.manifest.TransportProfileID == profileID && candidate.manifest.Generation <= existing.manifest.Generation {
			return Catalog{}, invalid("generation_stale")
		}
	}
	copy := c.clone()
	for id, existing := range copy.releases {
		if existing.state != Published || existing.manifest.TransportProfileID != profileID {
			continue
		}
		retired, err := existing.withState(Retired)
		if err != nil {
			return Catalog{}, err
		}
		copy.releases[id] = retired
	}
	published, err := candidate.withState(Published)
	if err != nil {
		return Catalog{}, err
	}
	copy.releases[releaseID] = published
	return copy, nil
}

func (c Catalog) Rollback(currentReleaseID string) (Catalog, string, error) {
	current, exists := c.releases[currentReleaseID]
	if !exists || current.state != Published {
		return Catalog{}, "", invalid("published_not_found")
	}
	selectedID := ""
	var selected Release
	for id, value := range c.releases {
		if value.state != Retired || value.manifest.TransportProfileID != current.manifest.TransportProfileID || value.manifest.Generation >= current.manifest.Generation {
			continue
		}
		if selectedID == "" || value.manifest.Generation > selected.manifest.Generation {
			selectedID, selected = id, value
		}
	}
	if selectedID == "" {
		return Catalog{}, "", invalid("rollback_point_missing")
	}
	copy := c.clone()
	retiredCurrent, err := current.withState(Retired)
	if err != nil {
		return Catalog{}, "", err
	}
	publishedSelected, err := selected.withState(Published)
	if err != nil {
		return Catalog{}, "", err
	}
	copy.releases[currentReleaseID], copy.releases[selectedID] = retiredCurrent, publishedSelected
	return copy, selectedID, nil
}

func (c Catalog) CurrentForProfile(profileID string) (Release, bool) {
	for _, value := range c.releases {
		if value.state == Published && value.manifest.TransportProfileID == profileID {
			return cloneRelease(value), true
		}
	}
	return Release{}, false
}

func (c Catalog) Current() (Release, bool) {
	ids := make([]string, 0, len(c.releases))
	for id := range c.releases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if c.releases[id].state == Published {
			return cloneRelease(c.releases[id]), true
		}
	}
	return Release{}, false
}

func (c Catalog) Get(releaseID string) (Release, bool) {
	value, exists := c.releases[releaseID]
	if !exists {
		return Release{}, false
	}
	return cloneRelease(value), true
}

func (c Catalog) clone() Catalog {
	copy := Catalog{releases: make(map[string]Release, len(c.releases))}
	for id, value := range c.releases {
		copy.releases[id] = cloneRelease(value)
	}
	return copy
}

type journal struct {
	SchemaVersion int            `json:"schema_version"`
	Entries       []journalEntry `json:"entries"`
}

type journalEntry struct {
	ReleaseID          string `json:"release_id"`
	ManifestSHA256     string `json:"manifest_sha256"`
	TransportProfileID string `json:"transport_profile_id"`
	Generation         uint64 `json:"generation"`
	State              State  `json:"state"`
}

func (c Catalog) Snapshot() ([]byte, error) {
	ids := make([]string, 0, len(c.releases))
	for id := range c.releases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	value := journal{SchemaVersion: 1, Entries: make([]journalEntry, 0, len(ids))}
	for _, id := range ids {
		r := c.releases[id]
		value.Entries = append(value.Entries, journalEntry{
			ReleaseID: id, ManifestSHA256: r.manifestSHA256, TransportProfileID: r.manifest.TransportProfileID,
			Generation: r.manifest.Generation, State: r.state,
		})
	}
	return json.Marshal(value)
}

func (c Catalog) Restore(raw []byte, releases []Release) (Catalog, error) {
	var value journal
	if len(raw) == 0 || len(raw) > maxManifestBytes || decodeCanonicalJSON(raw, &value) != nil || value.SchemaVersion != 1 {
		return Catalog{}, invalid("journal_invalid")
	}
	available := make(map[string]Release, len(releases))
	for _, r := range releases {
		available[r.manifest.ReleaseID] = r
	}
	restored := NewCatalog()
	lastID := ""
	publishedProfiles := map[string]struct{}{}
	for _, entry := range value.Entries {
		if entry.ReleaseID <= lastID || !validID(entry.ReleaseID) || !validSHA256(entry.ManifestSHA256) {
			return Catalog{}, invalid("journal_entry_invalid")
		}
		base, ok := available[entry.ReleaseID]
		if !ok || base.manifestSHA256 != entry.ManifestSHA256 || base.manifest.TransportProfileID != entry.TransportProfileID || base.manifest.Generation != entry.Generation {
			return Catalog{}, invalid("journal_binding_mismatch")
		}
		stateful, err := base.setState(entry.State)
		if err != nil {
			return Catalog{}, err
		}
		if entry.State == Published {
			if _, exists := publishedProfiles[entry.TransportProfileID]; exists {
				return Catalog{}, invalid("journal_multiple_published")
			}
			publishedProfiles[entry.TransportProfileID] = struct{}{}
		}
		restored.releases[entry.ReleaseID] = stateful
		lastID = entry.ReleaseID
	}
	if len(restored.releases) != len(available) {
		return Catalog{}, invalid("journal_release_set_mismatch")
	}
	return restored, nil
}
