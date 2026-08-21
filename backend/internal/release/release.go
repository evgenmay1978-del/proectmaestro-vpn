package release

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
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
	TransportSHA256       string     `json:"transport_sha256"`
	RuntimeMaterialSHA256 string     `json:"runtime_material_sha256"`
	EvidenceTrustSHA256   string     `json:"evidence_trust_sha256"`
	RuntimeConfigPath     string     `json:"runtime_config_path"`
	TargetPort            int        `json:"target_port"`
	FallbackProbePort     int        `json:"fallback_probe_port"`
	Artifacts             []Artifact `json:"artifacts"`
}

type CandidateSpec struct {
	Transport             controlplane.TransportRelease
	Generation            uint64
	XrayVersion           string
	XrayCommit            string
	XraySource            string
	XrayBinarySHA256      string
	RuntimeMaterialSHA256 string
	EvidenceTrust         EvidenceTrust
	ValidationEvidence    ValidationEvidence
	XrayBinary            []byte
	ConfigJSON            []byte
	SystemdUnit           []byte
	RollbackJSON          []byte
}

type Release struct {
	manifest              Manifest
	canonical             []byte
	manifestSHA256        string
	state                 State
	transport             controlplane.TransportRelease
	configTemplate        []byte
	runtimeMaterialSHA256 string
}

func NewCandidate(spec CandidateSpec) (Release, error) {
	if err := validateCandidateInputs(spec); err != nil {
		return Release{}, err
	}
	candidateSHA, err := CandidateSHA256(spec)
	if err != nil || !equalDigest(candidateSHA, spec.ValidationEvidence.CandidateSHA256) {
		return Release{}, invalid("evidence_candidate_mismatch")
	}
	binding, err := bindingForSpec(spec)
	if err != nil {
		return Release{}, err
	}
	now := time.Now().UTC()
	if err := validateEvidence(spec.ValidationEvidence, binding, spec.EvidenceTrust, &now); err != nil {
		return Release{}, err
	}
	evidenceJSON, err := marshalCanonical(spec.ValidationEvidence)
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
		XrayBinarySHA256: spec.XrayBinarySHA256, CandidateSHA256: candidateSHA,
		TransportSHA256: binding.transportSHA, RuntimeMaterialSHA256: binding.runtimeSHA,
		EvidenceTrustSHA256: binding.trustSHA, RuntimeConfigPath: RuntimeConfigPath,
		TargetPort: SidecarPort, FallbackProbePort: FallbackProbePort, Artifacts: artifacts,
	}
	if err := validateManifest(manifest); err != nil {
		return Release{}, err
	}
	canonical, err := marshalCanonical(manifest)
	if err != nil || len(canonical) > maxManifestBytes {
		return Release{}, invalid("manifest_encode")
	}
	return Release{
		manifest: cloneManifest(manifest), canonical: append([]byte(nil), canonical...),
		manifestSHA256: digestBytes(canonical), state: Candidate, transport: spec.Transport,
		configTemplate:        append([]byte(nil), spec.ConfigJSON...),
		runtimeMaterialSHA256: spec.RuntimeMaterialSHA256,
	}, nil
}

func RequiredValidationGates() []string { return append([]string(nil), requiredGates...) }

func validateCandidateInputs(spec CandidateSpec) error {
	if spec.Transport.State() != controlplane.TransportReleaseCandidate || spec.Generation == 0 ||
		!validID(spec.Transport.ID()) || !validID(spec.Transport.TransportProfileID()) ||
		!validID(spec.Transport.CompatibilityPresetID()) || !versionPattern.MatchString(spec.XrayVersion) ||
		!validCommit(spec.XrayCommit) || !validPinnedSource(spec.XraySource, spec.XrayCommit) ||
		!validSHA256(spec.RuntimeMaterialSHA256) || validateELF(spec.XrayBinary) != nil ||
		spec.EvidenceTrust.validate() != nil || ValidateConfigTemplate(spec.ConfigJSON) != nil ||
		ValidateSystemdTemplate(spec.SystemdUnit) != nil || ValidateRollbackTemplate(spec.RollbackJSON) != nil {
		return invalid("candidate_input_invalid")
	}
	if actual := digestBytes(spec.XrayBinary); !validSHA256(spec.XrayBinarySHA256) || !equalDigest(spec.XrayBinarySHA256, actual) {
		return invalid("xray_binary_digest_mismatch")
	}
	return nil
}

func ParseManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > maxManifestBytes || !utf8.Valid(raw) {
		return Manifest{}, invalid("manifest_bytes_invalid")
	}
	var manifest Manifest
	if err := decodeCanonicalJSON(raw, &manifest); err != nil {
		return Manifest{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return cloneManifest(manifest), nil
}

func (r Release) Manifest() Manifest                       { return cloneManifest(r.manifest) }
func (r Release) CanonicalManifest() []byte                { return append([]byte(nil), r.canonical...) }
func (r Release) ManifestSHA256() string                   { return r.manifestSHA256 }
func (r Release) State() State                             { return r.state }
func (r Release) Transport() controlplane.TransportRelease { return r.transport }
func (r Release) MaterializeRuntimeConfig(material RuntimeMaterial) ([]byte, error) {
	return materializeRuntimeConfig(r.configTemplate, r.transport, r.manifest.TransportSHA256, r.runtimeMaterialSHA256, material)
}

func (r Release) ValidateRuntimeConfig(raw []byte) error {
	return validateRuntimeConfig(raw, r.transport, r.manifest.TransportSHA256, r.runtimeMaterialSHA256)
}

func (r Release) VerifyArtifacts(_ map[string][]byte) error {
	return invalid("evidence_trust_required")
}

func (r Release) VerifyArtifactsWithTrust(artifacts map[string][]byte, trust EvidenceTrust) error {
	return r.verifyArtifactsWithTrustAt(artifacts, trust, nil)
}

func (r Release) verifyArtifactsWithTrustAt(artifacts map[string][]byte, trust EvidenceTrust, admissionTime *time.Time) error {
	trustSHA, err := trust.SHA256()
	if err != nil || !equalDigest(trustSHA, r.manifest.EvidenceTrustSHA256) {
		return invalid("evidence_trust_mismatch")
	}
	if len(artifacts) != len(r.manifest.Artifacts) {
		return invalid("artifact_set_mismatch")
	}
	var evidence ValidationEvidence
	for _, artifact := range r.manifest.Artifacts {
		data, ok := artifacts[artifact.Path]
		if !ok || int64(len(data)) != artifact.Size || !equalDigest(digestBytes(data), artifact.SHA256) {
			return invalid("artifact_digest_mismatch")
		}
		if err := validateArtifactContent(artifact.Path, data); err != nil {
			return err
		}
		if artifact.Path == "validation-report.json" {
			if err := decodeCanonicalJSON(data, &evidence); err != nil {
				return err
			}
		}
	}
	if xray, ok := artifacts["xray"]; !ok || !equalDigest(digestBytes(xray), r.manifest.XrayBinarySHA256) {
		return invalid("xray_manifest_mismatch")
	}
	recomputedCandidate, err := candidateSHA256FromManifestArtifacts(r.manifest, artifacts)
	if err != nil || !equalDigest(recomputedCandidate, r.manifest.CandidateSHA256) {
		return invalid("candidate_manifest_mismatch")
	}
	expected := evidenceBinding{
		candidateSHA: recomputedCandidate, transportSHA: r.manifest.TransportSHA256,
		runtimeSHA: r.manifest.RuntimeMaterialSHA256, xraySHA: r.manifest.XrayBinarySHA256,
		trustSHA: r.manifest.EvidenceTrustSHA256,
	}
	if err := validateEvidence(evidence, expected, trust, admissionTime); err != nil {
		return err
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
		!validPinnedSource(manifest.XraySource, manifest.XrayCommit) || !validSHA256(manifest.XrayBinarySHA256) ||
		!validSHA256(manifest.CandidateSHA256) || !validSHA256(manifest.TransportSHA256) ||
		!validSHA256(manifest.RuntimeMaterialSHA256) || !validSHA256(manifest.EvidenceTrustSHA256) ||
		manifest.RuntimeConfigPath != RuntimeConfigPath || manifest.TargetPort != SidecarPort ||
		manifest.FallbackProbePort != FallbackProbePort || len(manifest.Artifacts) != len(allowedArtifactPaths()) {
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
		return decodeCanonicalJSON(data, &evidence)
	case "xray":
		return validateELF(data)
	default:
		return invalid("artifact_path_invalid")
	}
}

func cloneManifest(manifest Manifest) Manifest {
	copy := manifest
	copy.Artifacts = append([]Artifact(nil), manifest.Artifacts...)
	return copy
}

func marshalCanonical(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	result := buffer.Bytes()
	if len(result) == 0 || result[len(result)-1] != '\n' {
		return nil, errors.New("canonical JSON encoder omitted newline")
	}
	return append([]byte(nil), result[:len(result)-1]...), nil
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

func validPinnedHTTPS(value string) (*url.URL, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" ||
		path.Clean(parsed.Path) != parsed.Path || strings.HasSuffix(parsed.Path, "/latest") {
		return nil, false
	}
	return parsed, true
}

func validPinnedSource(value, commit string) bool {
	parsed, ok := validPinnedHTTPS(value)
	if !ok || !validCommit(commit) || parsed.Host != "github.com" || parsed.Port() != "" {
		return false
	}
	prefix := "/XTLS/Xray-core/archive/" + commit
	return parsed.Path == prefix+".zip" || parsed.Path == prefix+".tar.gz"
}

func validateELF(raw []byte) error {
	if len(raw) == 0 || len(raw) > maxBinaryBytes {
		return invalid("xray_binary_invalid")
	}
	value, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil || value.Data != elf.ELFDATA2LSB || value.Entry == 0 ||
		(value.Type != elf.ET_EXEC && value.Type != elf.ET_DYN) ||
		!supportedELFClassMachine(value.Class, value.Machine) {
		return invalid("xray_binary_invalid")
	}
	hasExecutableEntry := false
	for _, program := range value.Progs {
		if program.Type != elf.PT_LOAD {
			continue
		}
		if !validELFLoad(program, uint64(len(raw))) {
			return invalid("xray_binary_invalid")
		}
		if program.Flags&elf.PF_X != 0 && program.Filesz > 0 &&
			value.Entry >= program.Vaddr && value.Entry < program.Vaddr+program.Filesz {
			hasExecutableEntry = true
		}
	}
	if !hasExecutableEntry {
		return invalid("xray_binary_invalid")
	}
	return nil
}

func supportedELFClassMachine(class elf.Class, machine elf.Machine) bool {
	return class == elf.ELFCLASS32 && (machine == elf.EM_386 || machine == elf.EM_ARM) ||
		class == elf.ELFCLASS64 && (machine == elf.EM_X86_64 || machine == elf.EM_AARCH64)
}

func validELFLoad(program *elf.Prog, rawSize uint64) bool {
	if program.Memsz == 0 || program.Filesz > program.Memsz || program.Off > rawSize ||
		program.Filesz > rawSize-program.Off || program.Vaddr > ^uint64(0)-program.Memsz {
		return false
	}
	if program.Align > 1 && (program.Align&(program.Align-1) != 0 || program.Vaddr%program.Align != program.Off%program.Align) {
		return false
	}
	return true
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

type Catalog struct {
	releases     map[string]Release
	predecessors map[string]string
	revision     uint64
}

func NewCatalog() Catalog {
	return Catalog{
		releases:     make(map[string]Release),
		predecessors: make(map[string]string),
		revision:     1,
	}
}

func (c Catalog) Revision() uint64 { return c.revision }

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
	if err := validateCatalog(c); err != nil {
		return Catalog{}, err
	}
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
	if err := bumpCatalogRevision(&copy); err != nil {
		return Catalog{}, err
	}
	return copy, nil
}

func (c Catalog) AddCandidate(candidate Release) (Catalog, error) {
	if candidate.state != Candidate {
		return Catalog{}, invalid("candidate_state_invalid")
	}
	return c.add(candidate)
}

func (c Catalog) add(value Release) (Catalog, error) {
	if err := validateCatalog(c); err != nil {
		return Catalog{}, err
	}
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
	if err := bumpCatalogRevision(&copy); err != nil {
		return Catalog{}, err
	}
	return copy, nil
}

func (c Catalog) Publish(releaseID string) (Catalog, error) {
	if err := validateCatalog(c); err != nil {
		return Catalog{}, err
	}
	candidate, exists := c.releases[releaseID]
	if !exists || candidate.state != Candidate {
		return Catalog{}, invalid("candidate_not_found")
	}
	profileID := candidate.manifest.TransportProfileID
	predecessorID := ""
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
		predecessorID = id
	}
	published, err := candidate.withState(Published)
	if err != nil {
		return Catalog{}, err
	}
	copy.releases[releaseID] = published
	copy.predecessors[releaseID] = predecessorID
	if err := bumpCatalogRevision(&copy); err != nil {
		return Catalog{}, err
	}
	return copy, nil
}

func (c Catalog) Rollback(currentReleaseID string) (Catalog, string, error) {
	if err := validateCatalog(c); err != nil {
		return Catalog{}, "", err
	}
	current, exists := c.releases[currentReleaseID]
	if !exists || current.state != Published {
		return Catalog{}, "", invalid("published_not_found")
	}
	selectedID, wasPublished := c.predecessors[currentReleaseID]
	selected, selectedExists := c.releases[selectedID]
	if !wasPublished || selectedID == "" || !selectedExists || selected.state != Retired {
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
	if err := bumpCatalogRevision(&copy); err != nil {
		return Catalog{}, "", err
	}
	return copy, selectedID, nil
}

func (c Catalog) CurrentForProfile(profileID string) (Release, bool) {
	var current Release
	found := false
	for _, value := range c.releases {
		if value.state == Published && value.manifest.TransportProfileID == profileID {
			if found {
				return Release{}, false
			}
			current, found = value, true
		}
	}
	if !found {
		return Release{}, false
	}
	return cloneRelease(current), true
}

func (c Catalog) Current() (Release, bool) {
	var current Release
	found := false
	for _, value := range c.releases {
		if value.state == Published {
			if found {
				return Release{}, false
			}
			current, found = value, true
		}
	}
	if !found {
		return Release{}, false
	}
	return cloneRelease(current), true
}

func (c Catalog) Get(releaseID string) (Release, bool) {
	value, exists := c.releases[releaseID]
	if !exists {
		return Release{}, false
	}
	return cloneRelease(value), true
}

func (c Catalog) clone() Catalog {
	copy := Catalog{
		releases:     make(map[string]Release, len(c.releases)),
		predecessors: make(map[string]string, len(c.predecessors)),
		revision:     c.revision,
	}
	for id, value := range c.releases {
		copy.releases[id] = cloneRelease(value)
	}
	for id, predecessorID := range c.predecessors {
		copy.predecessors[id] = predecessorID
	}
	return copy
}
