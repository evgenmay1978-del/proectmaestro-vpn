package release_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

const taskARuntimeMaterial = "synthetic-runtime-server-decryption"

func taskARuntimeMaterialValue(serverDecryption string) release.RuntimeMaterial {
	return release.RuntimeMaterial{
		ServerDecryption: serverDecryption,
		LocalExitID:      "exit-s1",
		RelayRoutes: []release.RelayRouteMaterial{
			{ExitID: "exit-s1", Address: "127.0.0.1", ServerName: "exit-s1.example.test", Credential: "00000000-0000-4000-8000-000000000011"},
			{ExitID: "exit-s2", Address: "192.0.2.12", ServerName: "exit-s2.example.test", Credential: "00000000-0000-4000-8000-000000000012"},
			{ExitID: "exit-s3", Address: "192.0.2.13", ServerName: "exit-s3.example.test", Credential: "00000000-0000-4000-8000-000000000013"},
			{ExitID: "exit-s4", Address: "192.0.2.14", ServerName: "exit-s4.example.test", Credential: "00000000-0000-4000-8000-000000000014"},
		},
	}
}

func taskAMinimalELF() []byte {
	const (
		elfHeaderSize     = 64
		programHeaderSize = 56
		loadAddress       = uint64(0x400000)
	)
	raw := make([]byte, elfHeaderSize+programHeaderSize)
	copy(raw[:4], []byte{0x7f, 'E', 'L', 'F'})
	raw[4], raw[5], raw[6], raw[7] = 2, 1, 1, 0
	binary.LittleEndian.PutUint16(raw[16:18], 2)
	binary.LittleEndian.PutUint16(raw[18:20], 62)
	binary.LittleEndian.PutUint32(raw[20:24], 1)
	binary.LittleEndian.PutUint64(raw[24:32], loadAddress+uint64(len(raw))-1)
	binary.LittleEndian.PutUint64(raw[32:40], elfHeaderSize)
	binary.LittleEndian.PutUint16(raw[52:54], elfHeaderSize)
	binary.LittleEndian.PutUint16(raw[54:56], programHeaderSize)
	binary.LittleEndian.PutUint16(raw[56:58], 1)

	program := raw[elfHeaderSize:]
	binary.LittleEndian.PutUint32(program[0:4], 1)
	binary.LittleEndian.PutUint32(program[4:8], 5)
	binary.LittleEndian.PutUint64(program[16:24], loadAddress)
	binary.LittleEndian.PutUint64(program[24:32], loadAddress)
	binary.LittleEndian.PutUint64(program[32:40], uint64(len(raw)))
	binary.LittleEndian.PutUint64(program[40:48], uint64(len(raw)))
	binary.LittleEndian.PutUint64(program[48:56], 0x1000)
	return raw
}

func taskATrust(t *testing.T) (release.EvidenceTrust, ed25519.PrivateKey) {
	t.Helper()
	seed := bytes.Repeat([]byte{0x31}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	trust, err := release.NewEvidenceTrust([]release.EvidenceTrustKey{{
		KeyID:        "task-a-test-key",
		PublicKey:    privateKey.Public().(ed25519.PublicKey),
		SourceOrigin: "https://evidence.example.invalid",
	}})
	if err != nil {
		t.Fatalf("NewEvidenceTrust: %v", err)
	}
	return trust, privateKey
}

func taskARuntimeCommitment(material string) string {
	value := struct {
		SchemaVersion int                     `json:"schema_version"`
		Purpose       string                  `json:"purpose"`
		Material      release.RuntimeMaterial `json:"material"`
	}{
		SchemaVersion: 1,
		Purpose:       "maestro-xray-cdn-server-decryption",
		Material:      taskARuntimeMaterialValue(material),
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		panic(err)
	}
	raw := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func taskASpec(t *testing.T, id string) (release.CandidateSpec, release.EvidenceTrust, ed25519.PrivateKey) {
	t.Helper()
	trust, privateKey := taskATrust(t)
	binaryBytes := taskAMinimalELF()
	binaryDigest := sha256.Sum256(binaryBytes)
	runtimeDigest, err := release.RuntimeMaterialSHA256(taskARuntimeMaterialValue(taskARuntimeMaterial))
	if err != nil {
		t.Fatalf("RuntimeMaterialSHA256: %v", err)
	}
	commit := strings.Repeat("a", 40)
	spec := release.CandidateSpec{
		Transport: validTransport(t, id), Generation: 1,
		XrayVersion: "26.7.28", XrayCommit: commit,
		XraySource:       "https://github.com/XTLS/Xray-core/archive/" + commit + ".zip",
		XrayBinarySHA256: hex.EncodeToString(binaryDigest[:]), XrayBinary: binaryBytes,
		RuntimeMaterialSHA256: runtimeDigest, EvidenceTrust: trust,
		ConfigJSON: release.DefaultConfigTemplate(), SystemdUnit: release.DefaultSystemdTemplate(),
		RollbackJSON: release.DefaultRollbackTemplate(),
	}
	return spec, trust, privateKey
}

func taskAResignReport(t *testing.T, report *release.GateReport, privateKey ed25519.PrivateKey) {
	t.Helper()
	payload, err := report.CanonicalUnsignedPayload()
	if err != nil {
		t.Fatalf("CanonicalUnsignedPayload(%s): %v", report.GateID, err)
	}
	report.Signature = hex.EncodeToString(ed25519.Sign(privateKey, payload))
}

func taskASignedReports(t *testing.T, spec release.CandidateSpec, privateKey ed25519.PrivateKey, observed time.Time) []release.GateReport {
	t.Helper()
	candidateSHA, err := release.CandidateSHA256(spec)
	if err != nil {
		t.Fatalf("CandidateSHA256: %v", err)
	}
	transportSHA, err := release.TransportSHA256(spec.Transport)
	if err != nil {
		t.Fatalf("TransportSHA256: %v", err)
	}
	reports := make([]release.GateReport, 0, len(release.RequiredValidationGates()))
	for _, gate := range release.RequiredValidationGates() {
		evidenceClass, ok := release.MinimumEvidenceClass(gate)
		if !ok {
			t.Fatalf("MinimumEvidenceClass(%s) not found", gate)
		}
		report := release.GateReport{
			SchemaVersion: release.GateReportSchemaVersion, GateID: gate,
			EvidenceClass: evidenceClass, CandidateSHA256: candidateSHA,
			TransportSHA256: transportSHA, RuntimeMaterialSHA256: spec.RuntimeMaterialSHA256,
			XrayBinarySHA256: spec.XrayBinarySHA256,
			Source:           "https://evidence.example.invalid/immutable/" + candidateSHA + "/" + gate + ".json",
			ObservedAt:       observed.UTC().Format(time.RFC3339Nano), Outcome: "PASS", KeyID: "task-a-test-key",
		}
		taskAResignReport(t, &report, privateKey)
		reports = append(reports, report)
	}
	return reports
}

func taskACompleteSpec(t *testing.T, id string) (release.CandidateSpec, release.EvidenceTrust, ed25519.PrivateKey) {
	t.Helper()
	spec, trust, privateKey := taskASpec(t, id)
	evidence, err := release.BuildValidationEvidence(spec, taskASignedReports(t, spec, privateKey, time.Now().UTC()))
	if err != nil {
		t.Fatalf("BuildValidationEvidence: %v", err)
	}
	spec.ValidationEvidence = evidence
	return spec, trust, privateKey
}

func taskAWriteSealedRelease(t *testing.T, spec release.CandidateSpec, candidate release.Release) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "release")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir release: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), candidate.CanonicalManifest(), 0o400); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for name, data := range artifactBytes(spec) {
		mode := os.FileMode(0o400)
		if name == "xray" {
			mode = 0o500
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, mode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("seal release directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil && !os.IsNotExist(err) {
			t.Errorf("restore release directory permissions: %v", err)
		}
	})
	return dir
}

func taskARebuildTransport(t *testing.T, current controlplane.TransportRelease, mutate func(*controlplane.TransportReleaseSpec)) controlplane.TransportRelease {
	t.Helper()
	spec := controlplane.TransportReleaseSpec{ID: current.ID(), Profile: current.Profile(), Preset: current.Preset(), State: current.State(), ApprovedEdges: current.ApprovedEdges()}
	mutate(&spec)
	value, err := controlplane.NewTransportRelease(spec)
	if err != nil {
		t.Fatalf("NewTransportRelease after mutation: %v", err)
	}
	return value
}

func TestTaskAEvidenceRejectsReplayAcrossImmutableBindings(t *testing.T) {
	base, _, _ := taskACompleteSpec(t, "release-task-a")
	mutations := map[string]func(*release.CandidateSpec){
		"profile": func(spec *release.CandidateSpec) {
			spec.Transport = taskARebuildTransport(t, spec.Transport, func(value *controlplane.TransportReleaseSpec) {
				value.Profile.PublicHost = "replayed.example.invalid"
			})
		},
		"preset": func(spec *release.CandidateSpec) {
			spec.Transport = taskARebuildTransport(t, spec.Transport, func(value *controlplane.TransportReleaseSpec) {
				value.Preset.FixtureRefs[0] = "fixture-replayed"
			})
		},
		"edge address": func(spec *release.CandidateSpec) {
			spec.Transport = taskARebuildTransport(t, spec.Transport, func(value *controlplane.TransportReleaseSpec) { value.ApprovedEdges[0].Address = "1.1.1.12" })
		},
		"edge evidence": func(spec *release.CandidateSpec) {
			spec.Transport = taskARebuildTransport(t, spec.Transport, func(value *controlplane.TransportReleaseSpec) {
				value.ApprovedEdges[0].EvidenceRef = "evidence-replayed"
			})
		},
		"edge time": func(spec *release.CandidateSpec) {
			spec.Transport = taskARebuildTransport(t, spec.Transport, func(value *controlplane.TransportReleaseSpec) {
				value.ApprovedEdges[0].ApprovedAt = value.ApprovedEdges[0].ApprovedAt.Add(time.Second)
			})
		},
		"runtime commitment": func(spec *release.CandidateSpec) { spec.RuntimeMaterialSHA256 = strings.Repeat("c", 64) },
		"candidate binding":  func(spec *release.CandidateSpec) { spec.XrayVersion = "26.7.29" },
		"binary": func(spec *release.CandidateSpec) {
			spec.XrayBinary = append([]byte(nil), spec.XrayBinary...)
			spec.XrayBinary[len(spec.XrayBinary)-1] ^= 1
			digest := sha256.Sum256(spec.XrayBinary)
			spec.XrayBinarySHA256 = hex.EncodeToString(digest[:])
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			spec := base
			mutate(&spec)
			if _, err := release.NewCandidate(spec); err == nil {
				t.Fatal("replayed evidence accepted after immutable binding changed")
			}
		})
	}
}

func TestTaskAEvidenceRejectsSelfAttestationAndMalformedReports(t *testing.T) {
	spec, _, privateKey := taskASpec(t, "release-task-a")
	valid := taskASignedReports(t, spec, privateKey, time.Now().UTC())
	cases := []struct {
		name   string
		mutate func(*release.GateReport)
		resign bool
	}{
		{name: "unsigned", mutate: func(report *release.GateReport) { report.Signature = "" }},
		{name: "unknown key", mutate: func(report *release.GateReport) { report.KeyID = "unknown-key" }, resign: true},
		{name: "wrong signature", mutate: func(report *release.GateReport) { report.Signature = strings.Repeat("0", ed25519.SignatureSize*2) }},
		{name: "stale", mutate: func(report *release.GateReport) {
			report.ObservedAt = time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
		}, resign: true},
		{name: "malformed source", mutate: func(report *release.GateReport) { report.Source = "http://evidence.example.invalid/report.json" }},
		{name: "malformed time", mutate: func(report *release.GateReport) { report.ObservedAt = "not-a-time" }},
		{name: "wrong outcome", mutate: func(report *release.GateReport) { report.Outcome = "FAIL" }},
		{name: "binding mismatch", mutate: func(report *release.GateReport) {
			report.CandidateSHA256 = strings.Repeat("c", 64)
			report.Source = "https://evidence.example.invalid/immutable/" + report.CandidateSHA256 + "/" + report.GateID + ".json"
		}, resign: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			reports := append([]release.GateReport(nil), valid...)
			test.mutate(&reports[0])
			if test.resign {
				taskAResignReport(t, &reports[0], privateKey)
			}
			if _, err := release.BuildValidationEvidence(spec, reports); err == nil {
				t.Fatal("invalid signed evidence accepted")
			}
		})
	}

	evidence, err := release.BuildValidationEvidence(spec, valid)
	if err != nil {
		t.Fatalf("BuildValidationEvidence(valid): %v", err)
	}
	evidence.Gates[0].ReportSHA256 = strings.Repeat("b", 64)
	spec.ValidationEvidence = evidence
	if _, err := release.NewCandidate(spec); err == nil {
		t.Fatal("self-attested report digest accepted")
	}

	for name, mutate := range map[string]func([]byte){
		"header only": func(raw []byte) {},
		"entry outside executable segment": func(raw []byte) {
			binary.LittleEndian.PutUint64(raw[24:32], 0x500000)
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := append([]byte(nil), taskAMinimalELF()...)
			if name == "header only" {
				invalid = invalid[:64]
			}
			mutate(invalid)
			spec, _, _ := taskASpec(t, "release-task-a")
			spec.XrayBinary = invalid
			digest := sha256.Sum256(invalid)
			spec.XrayBinarySHA256 = hex.EncodeToString(digest[:])
			if _, err := release.CandidateSHA256(spec); err == nil {
				t.Fatal("structurally invalid ELF accepted")
			}
		})
	}
}

func TestTaskAEvidenceTrustPinsCanonicalSourceOrigin(t *testing.T) {
	spec, _, privateKey := taskASpec(t, "release-task-a-origin")
	reports := taskASignedReports(t, spec, privateKey, time.Now().UTC())
	reports[0].Source = "https://other-evidence.example.invalid/immutable/" + reports[0].CandidateSHA256 + "/" + reports[0].GateID + ".json"
	taskAResignReport(t, &reports[0], privateKey)
	if _, err := release.BuildValidationEvidence(spec, reports); err == nil {
		t.Fatal("correctly signed report from an untrusted origin accepted")
	}

	publicKey := privateKey.Public().(ed25519.PublicKey)
	for _, sourceOrigin := range []string{
		"http://evidence.example.invalid",
		"https://evidence.example.invalid/",
		"https://evidence.example.invalid/path",
		"https://evidence.example.invalid:443",
		"https://127.0.0.1",
		"https://Evidence.example.invalid",
		"https://localhost",
	} {
		t.Run(sourceOrigin, func(t *testing.T) {
			if _, err := release.NewEvidenceTrust([]release.EvidenceTrustKey{{
				KeyID: "task-a-test-key", PublicKey: publicKey, SourceOrigin: sourceOrigin,
			}}); err == nil {
				t.Fatalf("non-canonical evidence origin accepted: %q", sourceOrigin)
			}
		})
	}
}

func TestTaskAPromotionRechecksEvidenceFreshness(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("promotion filesystem contract is covered by Linux CI")
	}
	observed := time.Now().UTC()
	spec, trust, privateKey := taskASpec(t, "release-task-a-freshness")
	evidence, err := release.BuildValidationEvidence(spec, taskASignedReports(t, spec, privateKey, observed))
	if err != nil {
		t.Fatalf("BuildValidationEvidence: %v", err)
	}
	spec.ValidationEvidence = evidence
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	dir := taskAWriteSealedRelease(t, spec, candidate)

	if err := release.ValidateReleaseDirectoryWithTrust(dir, trust); err != nil {
		t.Fatalf("historical validation unexpectedly expired evidence: %v", err)
	}
	if err := release.ValidateReleaseDirectoryForPromotionWithTrust(dir, trust, observed.Add(23*time.Hour)); err != nil {
		t.Fatalf("fresh promotion evidence rejected: %v", err)
	}
	if err := release.ValidateReleaseDirectoryForPromotionWithTrust(dir, trust, observed.Add(25*time.Hour)); err == nil {
		t.Fatal("stale evidence accepted at the promotion boundary")
	}
	if err := release.ValidateReleaseDirectoryForPromotionWithTrust(dir, trust, time.Time{}); err == nil {
		t.Fatal("zero promotion time accepted")
	}
}

func TestTaskARuntimeMaterializationRejectsDowngradeAndBoundaryDrift(t *testing.T) {
	spec, _, _ := taskACompleteSpec(t, "release-task-a")
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	config, err := candidate.MaterializeRuntimeConfig(taskARuntimeMaterialValue(taskARuntimeMaterial))
	if err != nil {
		t.Fatalf("MaterializeRuntimeConfig: %v", err)
	}
	profile := spec.Transport.Profile()
	if !bytes.Contains(config, []byte(profile.PublicHost)) || !bytes.Contains(config, []byte(profile.SecretPath)) || bytes.Contains(config, []byte("<RUNTIME_")) {
		t.Fatal("runtime config did not use the immutable transport host/path")
	}
	for _, material := range []string{"", "bad\nvalue", "different-material", string([]byte{0xff})} {
		if _, err := candidate.MaterializeRuntimeConfig(taskARuntimeMaterialValue(material)); err == nil {
			t.Fatalf("unsafe or uncommitted runtime material accepted: %q", material)
		}
	}
	for _, forbidden := range []string{"none", "<RUNTIME_SERVER_DECRYPTION>"} {
		t.Run("matching commitment "+forbidden, func(t *testing.T) {
			spec, _, privateKey := taskASpec(t, "release-task-a-forbidden")
			spec.RuntimeMaterialSHA256 = taskARuntimeCommitment(forbidden)
			evidence, err := release.BuildValidationEvidence(spec, taskASignedReports(t, spec, privateKey, time.Now().UTC()))
			if err != nil {
				t.Fatalf("BuildValidationEvidence: %v", err)
			}
			spec.ValidationEvidence = evidence
			candidate, err := release.NewCandidate(spec)
			if err != nil {
				t.Fatalf("NewCandidate: %v", err)
			}
			if _, err := candidate.MaterializeRuntimeConfig(taskARuntimeMaterialValue(forbidden)); err == nil {
				t.Fatalf("forbidden runtime material accepted with matching signed commitment: %q", forbidden)
			}
		})
	}

	drifts := map[string][]byte{
		"listener port": bytes.Replace(config, []byte(`"port":18081`), []byte(`"port":18080`), 1),
		"public host":   bytes.Replace(config, []byte(profile.PublicHost), []byte("drifted.example.invalid"), 1),
		"secret path":   bytes.Replace(config, []byte(profile.SecretPath), []byte("/drifted-secret-path"), 1),
	}
	for name, drifted := range drifts {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(config, drifted) {
				t.Fatal("test mutation did not change runtime config")
			}
			if err := candidate.ValidateRuntimeConfig(drifted); err == nil {
				t.Fatal("runtime boundary drift accepted")
			}
		})
	}
}

func TestTaskAArtifactVerificationRequiresExternalTrust(t *testing.T) {
	spec, trust, _ := taskACompleteSpec(t, "release-task-a")
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	artifacts := artifactBytes(spec)
	if err := candidate.VerifyArtifacts(artifacts); err == nil {
		t.Fatal("artifact verification succeeded without external trust")
	}
	if err := candidate.VerifyArtifactsWithTrust(artifacts, trust); err != nil {
		t.Fatalf("VerifyArtifactsWithTrust: %v", err)
	}
	otherPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x32}, ed25519.SeedSize))
	otherTrust, err := release.NewEvidenceTrust([]release.EvidenceTrustKey{{
		KeyID:        "task-a-other-key",
		PublicKey:    otherPrivate.Public().(ed25519.PublicKey),
		SourceOrigin: "https://evidence.example.invalid",
	}})
	if err != nil {
		t.Fatalf("NewEvidenceTrust(other): %v", err)
	}
	if err := candidate.VerifyArtifactsWithTrust(artifacts, otherTrust); err == nil {
		t.Fatal("mismatched external trust accepted")
	}
}
