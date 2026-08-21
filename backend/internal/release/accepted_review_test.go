package release_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func malformedSecondLoadELF() []byte {
	const (
		elfHeaderSize     = 64
		programHeaderSize = 56
		loadAddress       = uint64(0x400000)
	)
	raw := make([]byte, elfHeaderSize+2*programHeaderSize)
	copy(raw, taskAMinimalELF())
	binary.LittleEndian.PutUint16(raw[56:58], 2)
	binary.LittleEndian.PutUint64(raw[24:32], loadAddress+uint64(len(raw))-1)
	first := raw[elfHeaderSize : elfHeaderSize+programHeaderSize]
	binary.LittleEndian.PutUint64(first[32:40], uint64(len(raw)))
	binary.LittleEndian.PutUint64(first[40:48], uint64(len(raw)))
	second := raw[elfHeaderSize+programHeaderSize:]
	binary.LittleEndian.PutUint32(second[0:4], 1)
	binary.LittleEndian.PutUint32(second[4:8], 4)
	binary.LittleEndian.PutUint64(second[8:16], uint64(len(raw)+1))
	binary.LittleEndian.PutUint64(second[16:24], 0x500000)
	binary.LittleEndian.PutUint64(second[24:32], 0x500000)
	binary.LittleEndian.PutUint64(second[32:40], 1)
	binary.LittleEndian.PutUint64(second[40:48], 1)
	binary.LittleEndian.PutUint64(second[48:56], 0x1000)
	return raw
}

func TestTaskARuntimeConfigCarriesEveryServerRelevantXHTTPField(t *testing.T) {
	spec, _, _ := taskACompleteSpec(t, "release-task-a-xhttp")
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	config, err := candidate.MaterializeRuntimeConfig(release.RuntimeMaterial{ServerDecryption: taskARuntimeMaterial})
	if err != nil {
		t.Fatalf("MaterializeRuntimeConfig: %v", err)
	}
	required := [][]byte{
		[]byte(`"uplinkHTTPMethod":"GET"`), []byte(`"uplinkDataPlacement":"body"`),
		[]byte(`"sessionIDPlacement":"query"`), []byte(`"sessionIDKey":"auth"`),
		[]byte(`"sessionIDLength":16`), []byte(`"seqPlacement":"query"`), []byte(`"seqKey":"chunk_id"`),
	}
	for _, fragment := range required {
		if !bytes.Contains(config, fragment) {
			t.Fatalf("runtime config omitted XHTTP field %s", fragment)
		}
	}
	drifts := map[string][]byte{
		"uplink method":      bytes.Replace(config, []byte(`"uplinkHTTPMethod":"GET"`), []byte(`"uplinkHTTPMethod":"POST"`), 1),
		"uplink placement":   bytes.Replace(config, []byte(`"uplinkDataPlacement":"body"`), []byte(`"uplinkDataPlacement":"query"`), 1),
		"session placement":  bytes.Replace(config, []byte(`"sessionIDPlacement":"query"`), []byte(`"sessionIDPlacement":"header"`), 1),
		"session key":        bytes.Replace(config, []byte(`"sessionIDKey":"auth"`), []byte(`"sessionIDKey":"sid"`), 1),
		"session length":     bytes.Replace(config, []byte(`"sessionIDLength":16`), []byte(`"sessionIDLength":15`), 1),
		"sequence placement": bytes.Replace(config, []byte(`"seqPlacement":"query"`), []byte(`"seqPlacement":"header"`), 1),
		"sequence key":       bytes.Replace(config, []byte(`"seqKey":"chunk_id"`), []byte(`"seqKey":"sequence"`), 1),
	}
	for name, drifted := range drifts {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(config, drifted) {
				t.Fatal("test mutation did not change runtime config")
			}
			if err := candidate.ValidateRuntimeConfig(drifted); err == nil {
				t.Fatal("runtime XHTTP drift accepted")
			}
		})
	}
}

func TestTaskARuntimeMaterialRejectsNormalizedPlaceholdersWithMatchingCommitment(t *testing.T) {
	for _, material := range []string{"placeholder", "Change_Me", "replace-me", "TODO", "tbd", "your_server_decryption"} {
		t.Run(material, func(t *testing.T) {
			spec, _, privateKey := taskASpec(t, "release-task-a-placeholder")
			spec.RuntimeMaterialSHA256 = taskARuntimeCommitment(material)
			evidence, err := release.BuildValidationEvidence(spec, taskASignedReports(t, spec, privateKey, time.Now().UTC()))
			if err != nil {
				t.Fatalf("BuildValidationEvidence: %v", err)
			}
			spec.ValidationEvidence = evidence
			candidate, err := release.NewCandidate(spec)
			if err != nil {
				t.Fatalf("NewCandidate: %v", err)
			}
			if _, err := candidate.MaterializeRuntimeConfig(release.RuntimeMaterial{ServerDecryption: material}); err == nil {
				t.Fatal("normalized placeholder accepted with matching signed commitment")
			}
		})
	}
}

func TestTaskATransportBindingRejectsInvalidUTF8BeforeHashing(t *testing.T) {
	base := validTransport(t, "release-task-a-utf8")
	mutations := map[string]func(*controlplane.TransportReleaseSpec){
		"origin route id": func(spec *controlplane.TransportReleaseSpec) { spec.Profile.OriginRouteID = "origin-\xff" },
		"fixture ref":     func(spec *controlplane.TransportReleaseSpec) { spec.Preset.FixtureRefs[0] = "fixture-\xff" },
		"evidence ref":    func(spec *controlplane.TransportReleaseSpec) { spec.ApprovedEdges[0].EvidenceRef = "evidence-\xff" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			transport := taskARebuildTransport(t, base, mutate)
			if _, err := release.TransportSHA256(transport); err == nil {
				t.Fatal("transport binding normalized invalid UTF-8 before hashing")
			}
		})
	}
}

func TestTaskAELFRejectsMalformedLoadsAndClassMachineMismatch(t *testing.T) {
	cases := map[string]func([]byte) []byte{
		"malformed second load":  func([]byte) []byte { return malformedSecondLoadELF() },
		"class machine mismatch": func(raw []byte) []byte { binary.LittleEndian.PutUint16(raw[18:20], 3); return raw },
		"bad load alignment":     func(raw []byte) []byte { binary.LittleEndian.PutUint64(raw[64+48:64+56], 3); return raw },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec, _, _ := taskASpec(t, "release-task-a-elf")
			spec.XrayBinary = mutate(append([]byte(nil), spec.XrayBinary...))
			digest := sha256.Sum256(spec.XrayBinary)
			spec.XrayBinarySHA256 = hex.EncodeToString(digest[:])
			if _, err := release.CandidateSHA256(spec); err == nil {
				t.Fatal("malformed ELF accepted")
			}
		})
	}
}

func TestTaskAXraySourceRequiresExactOfficialImmutableArchive(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, suffix := range []string{".zip", ".tar.gz"} {
		spec, _, _ := taskASpec(t, "release-task-a-source")
		spec.XraySource = "https://github.com/XTLS/Xray-core/archive/" + commit + suffix
		if _, err := release.CandidateSHA256(spec); err != nil {
			t.Fatalf("official immutable source %q rejected: %v", suffix, err)
		}
	}
	invalidSources := map[string]string{
		"wrong repository": "https://github.com/XTLS/Other/archive/" + commit + ".zip",
		"commit substring": "https://github.com/XTLS/Xray-core/archive/prefix-" + commit + ".zip",
		"latest-like path": "https://github.com/XTLS/Xray-core/archive/" + commit + "/latest.zip",
		"explicit port":    "https://github.com:443/XTLS/Xray-core/archive/" + commit + ".zip",
		"query":            "https://github.com/XTLS/Xray-core/archive/" + commit + ".zip?download=1",
	}
	for name, source := range invalidSources {
		t.Run(name, func(t *testing.T) {
			spec, _, _ := taskASpec(t, "release-task-a-source")
			spec.XraySource = source
			if _, err := release.CandidateSHA256(spec); err == nil {
				t.Fatal("non-official or mutable Xray source accepted")
			}
		})
	}
}
