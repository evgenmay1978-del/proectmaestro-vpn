package release

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"
	"time"
)

const evidenceMaxAge = 24 * time.Hour

type EvidenceTrustKey struct {
	KeyID        string `json:"key_id"`
	PublicKey    []byte `json:"public_key"`
	SourceOrigin string `json:"source_origin"`
}

type EvidenceTrust struct {
	SchemaVersion int                `json:"schema_version"`
	Keys          []EvidenceTrustKey `json:"keys"`
}

func NewEvidenceTrust(keys []EvidenceTrustKey) (EvidenceTrust, error) {
	copyKeys := cloneTrustKeys(keys)
	sort.Slice(copyKeys, func(i, j int) bool { return copyKeys[i].KeyID < copyKeys[j].KeyID })
	trust := EvidenceTrust{SchemaVersion: 1, Keys: copyKeys}
	if err := trust.validate(); err != nil {
		return EvidenceTrust{}, err
	}
	return trust, nil
}

func ParseEvidenceTrust(raw []byte) (EvidenceTrust, error) {
	var trust EvidenceTrust
	if len(raw) == 0 || len(raw) > maxManifestBytes || decodeCanonicalJSON(raw, &trust) != nil {
		return EvidenceTrust{}, invalid("evidence_trust_invalid")
	}
	if err := trust.validate(); err != nil {
		return EvidenceTrust{}, err
	}
	return EvidenceTrust{SchemaVersion: trust.SchemaVersion, Keys: cloneTrustKeys(trust.Keys)}, nil
}

func (trust EvidenceTrust) CanonicalJSON() ([]byte, error) {
	if err := trust.validate(); err != nil {
		return nil, err
	}
	raw, err := marshalCanonical(EvidenceTrust{
		SchemaVersion: trust.SchemaVersion,
		Keys:          cloneTrustKeys(trust.Keys),
	})
	if err != nil {
		return nil, invalid("evidence_trust_encode")
	}
	return raw, nil
}

func (trust EvidenceTrust) SHA256() (string, error) {
	raw, err := trust.CanonicalJSON()
	if err != nil {
		return "", err
	}
	return digestBytes(raw), nil
}

func (trust EvidenceTrust) validate() error {
	if trust.SchemaVersion != 1 || len(trust.Keys) == 0 || len(trust.Keys) > 64 {
		return invalid("evidence_trust_invalid")
	}
	last := ""
	publicKeys := make(map[string]struct{}, len(trust.Keys))
	for _, key := range trust.Keys {
		if !validID(key.KeyID) || key.KeyID <= last || len(key.PublicKey) != ed25519.PublicKeySize ||
			allZero(key.PublicKey) || !validEvidenceOrigin(key.SourceOrigin) {
			return invalid("evidence_trust_key_invalid")
		}
		encoded := string(key.PublicKey)
		if _, exists := publicKeys[encoded]; exists {
			return invalid("evidence_trust_key_duplicate")
		}
		publicKeys[encoded] = struct{}{}
		last = key.KeyID
	}
	return nil
}

func (trust EvidenceTrust) verifier(keyID string) (ed25519.PublicKey, string, bool) {
	if trust.validate() != nil {
		return nil, "", false
	}
	index := sort.Search(len(trust.Keys), func(i int) bool { return trust.Keys[i].KeyID >= keyID })
	if index >= len(trust.Keys) || trust.Keys[index].KeyID != keyID {
		return nil, "", false
	}
	key := trust.Keys[index]
	return append(ed25519.PublicKey(nil), key.PublicKey...), key.SourceOrigin, true
}

type GateReport struct {
	SchemaVersion         int    `json:"schema_version"`
	GateID                string `json:"gate_id"`
	CandidateSHA256       string `json:"candidate_sha256"`
	TransportSHA256       string `json:"transport_sha256"`
	RuntimeMaterialSHA256 string `json:"runtime_material_sha256"`
	XrayBinarySHA256      string `json:"xray_binary_sha256"`
	Source                string `json:"source"`
	ObservedAt            string `json:"observed_at"`
	Outcome               string `json:"outcome"`
	KeyID                 string `json:"key_id"`
	Signature             string `json:"signature"`
}

type unsignedGateReport struct {
	SchemaVersion         int    `json:"schema_version"`
	GateID                string `json:"gate_id"`
	CandidateSHA256       string `json:"candidate_sha256"`
	TransportSHA256       string `json:"transport_sha256"`
	RuntimeMaterialSHA256 string `json:"runtime_material_sha256"`
	XrayBinarySHA256      string `json:"xray_binary_sha256"`
	Source                string `json:"source"`
	ObservedAt            string `json:"observed_at"`
	Outcome               string `json:"outcome"`
	KeyID                 string `json:"key_id"`
}

func (report GateReport) CanonicalUnsignedPayload() ([]byte, error) {
	if err := validateGateMetadata(report); err != nil {
		return nil, err
	}
	return marshalCanonical(unsignedGateReport{
		SchemaVersion: report.SchemaVersion, GateID: report.GateID,
		CandidateSHA256: report.CandidateSHA256, TransportSHA256: report.TransportSHA256,
		RuntimeMaterialSHA256: report.RuntimeMaterialSHA256,
		XrayBinarySHA256:      report.XrayBinarySHA256, Source: report.Source,
		ObservedAt: report.ObservedAt, Outcome: report.Outcome, KeyID: report.KeyID,
	})
}

type GateEvidence struct {
	GateID       string     `json:"gate_id"`
	ReportSHA256 string     `json:"report_sha256"`
	Report       GateReport `json:"report"`
}

type ValidationEvidence struct {
	SchemaVersion         int            `json:"schema_version"`
	CandidateSHA256       string         `json:"candidate_sha256"`
	TransportSHA256       string         `json:"transport_sha256"`
	RuntimeMaterialSHA256 string         `json:"runtime_material_sha256"`
	XrayBinarySHA256      string         `json:"xray_binary_sha256"`
	EvidenceTrustSHA256   string         `json:"evidence_trust_sha256"`
	Gates                 []GateEvidence `json:"gates"`
}

type evidenceBinding struct {
	candidateSHA string
	transportSHA string
	runtimeSHA   string
	xraySHA      string
	trustSHA     string
}

func BuildValidationEvidence(spec CandidateSpec, reports []GateReport) (ValidationEvidence, error) {
	binding, err := bindingForSpec(spec)
	if err != nil {
		return ValidationEvidence{}, err
	}
	if len(reports) != len(requiredGates) {
		return ValidationEvidence{}, invalid("validation_evidence_invalid")
	}
	ordered := append([]GateReport(nil), reports...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].GateID < ordered[j].GateID })
	evidence := ValidationEvidence{
		SchemaVersion: 1, CandidateSHA256: binding.candidateSHA,
		TransportSHA256: binding.transportSHA, RuntimeMaterialSHA256: binding.runtimeSHA,
		XrayBinarySHA256: binding.xraySHA, EvidenceTrustSHA256: binding.trustSHA,
		Gates: make([]GateEvidence, 0, len(ordered)),
	}
	now := time.Now().UTC()
	for _, report := range ordered {
		if err := validateSignedGate(report, binding, spec.EvidenceTrust, &now); err != nil {
			return ValidationEvidence{}, err
		}
		raw, err := marshalCanonical(report)
		if err != nil {
			return ValidationEvidence{}, invalid("validation_report_encode")
		}
		evidence.Gates = append(evidence.Gates, GateEvidence{
			GateID: report.GateID, ReportSHA256: digestBytes(raw), Report: report,
		})
	}
	if err := validateEvidence(evidence, binding, spec.EvidenceTrust, &now); err != nil {
		return ValidationEvidence{}, err
	}
	return evidence, nil
}

func bindingForSpec(spec CandidateSpec) (evidenceBinding, error) {
	candidateSHA, err := CandidateSHA256(spec)
	if err != nil {
		return evidenceBinding{}, err
	}
	transportSHA, err := TransportSHA256(spec.Transport)
	if err != nil {
		return evidenceBinding{}, err
	}
	trustSHA, err := spec.EvidenceTrust.SHA256()
	if err != nil {
		return evidenceBinding{}, err
	}
	return evidenceBinding{
		candidateSHA: candidateSHA, transportSHA: transportSHA,
		runtimeSHA: spec.RuntimeMaterialSHA256, xraySHA: spec.XrayBinarySHA256,
		trustSHA: trustSHA,
	}, nil
}

// admissionTime is non-nil only while constructing/admitting a candidate.
// Historical artifact verification authenticates the signed observation time
// but deliberately does not make an immutable release expire as wall time moves.
func validateEvidence(evidence ValidationEvidence, expected evidenceBinding, trust EvidenceTrust, admissionTime *time.Time) error {
	if evidence.SchemaVersion != 1 || len(evidence.Gates) != len(requiredGates) ||
		!equalDigest(evidence.CandidateSHA256, expected.candidateSHA) ||
		!equalDigest(evidence.TransportSHA256, expected.transportSHA) ||
		!equalDigest(evidence.RuntimeMaterialSHA256, expected.runtimeSHA) ||
		!equalDigest(evidence.XrayBinarySHA256, expected.xraySHA) ||
		!equalDigest(evidence.EvidenceTrustSHA256, expected.trustSHA) {
		return invalid("validation_evidence_invalid")
	}
	for index, gate := range requiredGates {
		entry := evidence.Gates[index]
		if entry.GateID != gate || entry.Report.GateID != gate || !validSHA256(entry.ReportSHA256) {
			return invalid("validation_gate_missing")
		}
		raw, err := marshalCanonical(entry.Report)
		if err != nil || !equalDigest(entry.ReportSHA256, digestBytes(raw)) {
			return invalid("validation_report_digest_mismatch")
		}
		if err := validateSignedGate(entry.Report, expected, trust, admissionTime); err != nil {
			return err
		}
	}
	return nil
}

func validateSignedGate(report GateReport, expected evidenceBinding, trust EvidenceTrust, admissionTime *time.Time) error {
	if err := validateGateMetadata(report); err != nil {
		return err
	}
	if admissionTime != nil {
		observed, _ := time.Parse(time.RFC3339Nano, report.ObservedAt)
		if observed.After(admissionTime.Add(5*time.Minute)) || admissionTime.Sub(observed) > evidenceMaxAge {
			return invalid("validation_report_stale")
		}
	}
	if !equalDigest(report.CandidateSHA256, expected.candidateSHA) ||
		!equalDigest(report.TransportSHA256, expected.transportSHA) ||
		!equalDigest(report.RuntimeMaterialSHA256, expected.runtimeSHA) ||
		!equalDigest(report.XrayBinarySHA256, expected.xraySHA) {
		return invalid("validation_report_binding_mismatch")
	}
	key, sourceOrigin, ok := trust.verifier(report.KeyID)
	signature, err := hex.DecodeString(report.Signature)
	if !ok || !validEvidenceSourceForOrigin(report, sourceOrigin) || err != nil ||
		len(signature) != ed25519.SignatureSize || report.Signature != strings.ToLower(report.Signature) {
		return invalid("validation_report_signature_invalid")
	}
	payload, err := report.CanonicalUnsignedPayload()
	if err != nil || !ed25519.Verify(key, payload, signature) {
		return invalid("validation_report_signature_invalid")
	}
	return nil
}

func validateGateMetadata(report GateReport) error {
	observed, err := time.Parse(time.RFC3339Nano, report.ObservedAt)
	if report.SchemaVersion != 1 || !validGateID(report.GateID) ||
		!validSHA256(report.CandidateSHA256) || !validSHA256(report.TransportSHA256) ||
		!validSHA256(report.RuntimeMaterialSHA256) || !validSHA256(report.XrayBinarySHA256) ||
		!validImmutableEvidenceSource(report) || err != nil || observed.Location() != time.UTC ||
		observed.Format(time.RFC3339Nano) != report.ObservedAt || report.Outcome != "PASS" ||
		!validID(report.KeyID) {
		return invalid("validation_report_invalid")
	}
	return nil
}

func validGateID(value string) bool {
	index := sort.SearchStrings(requiredGates, value)
	return index < len(requiredGates) && requiredGates[index] == value
}

func validImmutableEvidenceSource(report GateReport) bool {
	parsed, ok := validPinnedHTTPS(report.Source)
	if !ok || parsed.Port() != "" || pathpkg.Clean(parsed.Path) != parsed.Path {
		return false
	}
	return parsed.Path == evidenceReportPath(report)
}

func validEvidenceSourceForOrigin(report GateReport, sourceOrigin string) bool {
	return validEvidenceOrigin(sourceOrigin) && report.Source == sourceOrigin+evidenceReportPath(report)
}

func evidenceReportPath(report GateReport) string {
	return "/immutable/" + report.CandidateSHA256 + "/" + report.GateID + ".json"
}

func validEvidenceOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return false
	}
	host := parsed.Hostname()
	if host == "" || host != strings.ToLower(host) || net.ParseIP(host) != nil || value != "https://"+host {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, current := range label {
			if (current < 'a' || current > 'z') && (current < '0' || current > '9') && current != '-' {
				return false
			}
		}
	}
	return true
}

func equalDigest(left, right string) bool {
	return validSHA256(left) && validSHA256(right) &&
		subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func cloneTrustKeys(keys []EvidenceTrustKey) []EvidenceTrustKey {
	copyKeys := make([]EvidenceTrustKey, len(keys))
	for index, key := range keys {
		copyKeys[index] = EvidenceTrustKey{
			KeyID: key.KeyID, PublicKey: append([]byte(nil), key.PublicKey...), SourceOrigin: key.SourceOrigin,
		}
	}
	return copyKeys
}

func allZero(value []byte) bool {
	var combined byte
	for _, item := range value {
		combined |= item
	}
	return combined == 0
}
