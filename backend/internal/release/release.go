package release

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
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
	maxManifestBytes  = 64 << 10
	maxBinaryBytes    = 256 << 20
)

var (
	ErrInvalidRelease = errors.New("release: invalid isolated release")
	safeIDPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)
	versionPattern     = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
)

type State string

const (
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
	TargetPort            int        `json:"target_port"`
	FallbackProbePort     int        `json:"fallback_probe_port"`
	Artifacts             []Artifact `json:"artifacts"`
}

type CandidateSpec struct {
	Transport    controlplane.TransportRelease
	Generation   uint64
	XrayVersion  string
	XrayCommit   string
	XrayBinary   []byte
	ConfigJSON   []byte
	SystemdUnit  []byte
	RollbackJSON []byte
}

type Release struct {
	manifest       Manifest
	canonical      []byte
	manifestSHA256 string
	state          State
	transport      controlplane.TransportRelease
}

func NewCandidate(spec CandidateSpec) (Release, error) {
	if spec.Transport.State() != controlplane.TransportReleaseCandidate ||
		spec.Generation == 0 || !validID(spec.Transport.ID()) ||
		!validID(spec.Transport.TransportProfileID()) ||
		!validID(spec.Transport.CompatibilityPresetID()) ||
		!versionPattern.MatchString(spec.XrayVersion) || !validCommit(spec.XrayCommit) ||
		len(spec.XrayBinary) == 0 || len(spec.XrayBinary) > maxBinaryBytes ||
		ValidateConfigTemplate(spec.ConfigJSON) != nil ||
		ValidateSystemdTemplate(spec.SystemdUnit) != nil ||
		ValidateRollbackTemplate(spec.RollbackJSON) != nil {
		return Release{}, ErrInvalidRelease
	}

	artifacts := artifactManifest(map[string][]byte{
		"config.json":                 spec.ConfigJSON,
		"maestro-xray-cdn.service":   spec.SystemdUnit,
		"rollback.json":               spec.RollbackJSON,
		"xray":                        spec.XrayBinary,
	})
	manifest := Manifest{
		SchemaVersion:         SchemaVersion,
		ReleaseID:             spec.Transport.ID(),
		Generation:            spec.Generation,
		TransportProfileID:    spec.Transport.TransportProfileID(),
		CompatibilityPresetID: spec.Transport.CompatibilityPresetID(),
		XrayVersion:           spec.XrayVersion,
		XrayCommit:            spec.XrayCommit,
		TargetPort:            SidecarPort,
		FallbackProbePort:     FallbackProbePort,
		Artifacts:             artifacts,
	}
	if err := validateManifest(manifest); err != nil {
		return Release{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || len(canonical) > maxManifestBytes {
		return Release{}, ErrInvalidRelease
	}
	digest := sha256.Sum256(canonical)
	return Release{
		manifest:       cloneManifest(manifest),
		canonical:      append([]byte(nil), canonical...),
		manifestSHA256: hex.EncodeToString(digest[:]),
		state:          Candidate,
		transport:      spec.Transport,
	}, nil
}

func ParseManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > maxManifestBytes || !utf8.Valid(raw) {
		return Manifest{}, ErrInvalidRelease
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, ErrInvalidRelease
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, ErrInvalidRelease
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Manifest{}, ErrInvalidRelease
	}
	return cloneManifest(manifest), nil
}

func (release Release) Manifest() Manifest { return cloneManifest(release.manifest) }

func (release Release) CanonicalManifest() []byte {
	return append([]byte(nil), release.canonical...)
}

func (release Release) ManifestSHA256() string { return release.manifestSHA256 }

func (release Release) State() State { return release.state }

func (release Release) Transport() controlplane.TransportRelease { return release.transport }

func (release Release) VerifyArtifacts(artifacts map[string][]byte) error {
	if len(artifacts) != len(release.manifest.Artifacts) {
		return ErrInvalidRelease
	}
	for _, artifact := range release.manifest.Artifacts {
		data, ok := artifacts[artifact.Path]
		if !ok || int64(len(data)) != artifact.Size {
			return ErrInvalidRelease
		}
		digest := sha256.Sum256(data)
		actual := hex.EncodeToString(digest[:])
		if subtle.ConstantTimeCompare([]byte(actual), []byte(artifact.SHA256)) != 1 {
			return ErrInvalidRelease
		}
		if err := validateArtifactContent(artifact.Path, data); err != nil {
			return err
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
		value := values[path]
		digest := sha256.Sum256(value)
		artifacts = append(artifacts, Artifact{
			Path: path, Size: int64(len(value)), SHA256: hex.EncodeToString(digest[:]),
		})
	}
	return artifacts
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion || manifest.Generation == 0 ||
		!validID(manifest.ReleaseID) || !validID(manifest.TransportProfileID) ||
		!validID(manifest.CompatibilityPresetID) ||
		!versionPattern.MatchString(manifest.XrayVersion) || !validCommit(manifest.XrayCommit) ||
		manifest.TargetPort != SidecarPort || manifest.FallbackProbePort != FallbackProbePort ||
		len(manifest.Artifacts) != len(allowedArtifactPaths()) {
		return ErrInvalidRelease
	}
	allowed := allowedArtifactPaths()
	for index, artifact := range manifest.Artifacts {
		if artifact.Path != allowed[index] || artifact.Size <= 0 || artifact.Size > artifactSizeLimit(artifact.Path) ||
			!validSHA256(artifact.SHA256) {
			return ErrInvalidRelease
		}
	}
	return nil
}

func allowedArtifactPaths() []string {
	return []string{"config.json", "maestro-xray-cdn.service", "rollback.json", "xray"}
}

func artifactSizeLimit(path string) int64 {
	switch path {
	case "config.json":
		return 1 << 20
	case "maestro-xray-cdn.service", "rollback.json":
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
	case "xray":
		if len(data) == 0 || len(data) > maxBinaryBytes {
			return ErrInvalidRelease
		}
		return nil
	default:
		return ErrInvalidRelease
	}
}

func cloneManifest(manifest Manifest) Manifest {
	copy := manifest
	copy.Artifacts = append([]Artifact(nil), manifest.Artifacts...)
	return copy
}

func validID(value string) bool {
	return value == strings.TrimSpace(value) && !strings.Contains(value, "..") && safeIDPattern.MatchString(value)
}

func validCommit(value string) bool {
	if len(value) != 40 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 20
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func (release Release) withState(state State) (Release, error) {
	allowed := release.state == Candidate && state == Published ||
		release.state == Published && state == Retired ||
		release.state == Retired && state == Published
	if !allowed {
		return Release{}, ErrInvalidRelease
	}
	transportState := controlplane.TransportReleaseCandidate
	switch state {
	case Published:
		transportState = controlplane.TransportReleasePublished
	case Retired:
		transportState = controlplane.TransportReleaseRetired
	default:
		return Release{}, ErrInvalidRelease
	}
	transport, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID: release.transport.ID(), Profile: release.transport.Profile(),
		Preset: release.transport.Preset(), State: transportState,
		ApprovedEdges: release.transport.ApprovedEdges(),
	})
	if err != nil {
		return Release{}, ErrInvalidRelease
	}
	copy := cloneRelease(release)
	copy.state = state
	copy.transport = transport
	return copy, nil
}

func cloneRelease(release Release) Release {
	copy := release
	copy.manifest = cloneManifest(release.manifest)
	copy.canonical = append([]byte(nil), release.canonical...)
	return copy
}

type Catalog struct {
	releases map[string]Release
}

func NewCatalog() Catalog { return Catalog{releases: make(map[string]Release)} }

func (catalog Catalog) AddCandidate(candidate Release) (Catalog, error) {
	if candidate.state != Candidate || !validID(candidate.manifest.ReleaseID) {
		return Catalog{}, ErrInvalidRelease
	}
	copy := catalog.clone()
	if _, exists := copy.releases[candidate.manifest.ReleaseID]; exists {
		return Catalog{}, ErrInvalidRelease
	}
	for _, existing := range copy.releases {
		if existing.manifest.Generation == candidate.manifest.Generation ||
			existing.manifestSHA256 == candidate.manifestSHA256 {
			return Catalog{}, ErrInvalidRelease
		}
	}
	copy.releases[candidate.manifest.ReleaseID] = cloneRelease(candidate)
	return copy, nil
}

func (catalog Catalog) Publish(releaseID string) (Catalog, error) {
	candidate, exists := catalog.releases[releaseID]
	if !exists || candidate.state != Candidate {
		return Catalog{}, ErrInvalidRelease
	}
	for _, existing := range catalog.releases {
		if existing.state == Published && candidate.manifest.Generation <= existing.manifest.Generation {
			return Catalog{}, ErrInvalidRelease
		}
	}
	copy := catalog.clone()
	for id, existing := range copy.releases {
		if existing.state != Published {
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

func (catalog Catalog) Rollback(currentReleaseID string) (Catalog, string, error) {
	current, exists := catalog.releases[currentReleaseID]
	if !exists || current.state != Published {
		return Catalog{}, "", ErrInvalidRelease
	}
	selectedID := ""
	var selected Release
	for id, candidate := range catalog.releases {
		if candidate.state != Retired || candidate.manifest.Generation >= current.manifest.Generation {
			continue
		}
		if selectedID == "" || candidate.manifest.Generation > selected.manifest.Generation {
			selectedID = id
			selected = candidate
		}
	}
	if selectedID == "" {
		return Catalog{}, "", ErrInvalidRelease
	}
	copy := catalog.clone()
	retiredCurrent, err := current.withState(Retired)
	if err != nil {
		return Catalog{}, "", err
	}
	publishedSelected, err := selected.withState(Published)
	if err != nil {
		return Catalog{}, "", err
	}
	copy.releases[currentReleaseID] = retiredCurrent
	copy.releases[selectedID] = publishedSelected
	return copy, selectedID, nil
}

func (catalog Catalog) Current() (Release, bool) {
	for _, release := range catalog.releases {
		if release.state == Published {
			return cloneRelease(release), true
		}
	}
	return Release{}, false
}

func (catalog Catalog) Get(releaseID string) (Release, bool) {
	release, exists := catalog.releases[releaseID]
	if !exists {
		return Release{}, false
	}
	return cloneRelease(release), true
}

func (catalog Catalog) clone() Catalog {
	copy := Catalog{releases: make(map[string]Release, len(catalog.releases))}
	for id, release := range catalog.releases {
		copy.releases[id] = cloneRelease(release)
	}
	return copy
}
