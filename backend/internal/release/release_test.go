package release_test

import (
	"bytes"
	"crypto/sha256"
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

func validTransport(t *testing.T, id string) controlplane.TransportRelease {
	t.Helper()
	profileID := "profile-main"
	presetID := "preset-main"
	profile := controlplane.TransportProfile{
		ID: profileID, PublicHost: "cdn.example.invalid",
		SecretPath: "/static/test/segment.ts/opaque", OriginRouteID: "origin-" + id,
		CompatibilityPresetID: presetID,
	}
	preset := controlplane.CompatibilityPreset{
		ID: presetID, Version: 1, Kind: "MAESTRO_ADVANCED", ProtectionLevel: "advanced",
		Capabilities: []string{"vless-encryption", "xhttp-get-body"},
		CoreRange:    "xray>=26.7.28", ClientRanges: []string{"maestrovpn>=154"}, FixtureRefs: []string{"fixture-a"},
		Protocol: "vless", Network: "xhttp", Port: 443, TLS: true,
		Mode: "packet-up", UplinkHTTPMethod: "GET", UplinkDataPlacement: "body",
		ALPN: []string{"h2"}, Fingerprint: "firefox",
		ExtraJSON:   `{"sessionIDPlacement":"query","sessionIDKey":"auth","sessionIDLength":16,"seqPlacement":"query","seqKey":"chunk_id"}`,
		LabelPrefix: "БС/Yandex", DomainFallback: true,
	}
	transport, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID: id, Profile: profile, Preset: preset, State: controlplane.TransportReleaseCandidate,
		ApprovedEdges: []controlplane.ApprovedEdge{{
			ID: "edge-" + id, TransportProfileID: profileID, Address: "1.1.1.11",
			ApprovedAt: time.Unix(10, 0), EvidenceRef: "evidence-" + id,
		}},
	})
	if err != nil {
		t.Fatalf("NewTransportRelease: %v", err)
	}
	return transport
}

func candidateSpec(t *testing.T, id string, generation uint64) release.CandidateSpec {
	t.Helper()
	binary := append([]byte{0x7f, 'E', 'L', 'F'}, bytes.Repeat([]byte{0}, 60)...)
	binaryDigest := sha256.Sum256(binary)
	spec := release.CandidateSpec{
		Transport: validTransport(t, id), Generation: generation,
		XrayVersion: "26.7.28", XrayCommit: strings.Repeat("a", 40),
		XraySource:       "https://github.com/XTLS/Xray-core/releases/download/v26.7.28/Xray-linux-64.zip",
		XrayBinarySHA256: hex.EncodeToString(binaryDigest[:]), XrayBinary: binary,
		ConfigJSON: release.DefaultConfigTemplate(), SystemdUnit: release.DefaultSystemdTemplate(),
		RollbackJSON: release.DefaultRollbackTemplate(),
	}
	gates := make(map[string]string)
	for _, gate := range release.RequiredValidationGates() {
		gates[gate] = strings.Repeat("b", 64)
	}
	evidence, err := release.BuildValidationEvidence(spec, gates)
	if err != nil {
		t.Fatalf("BuildValidationEvidence: %v", err)
	}
	spec.ValidationEvidence = evidence
	return spec
}
func mustCandidate(t *testing.T, id string, generation uint64) release.Release {
	t.Helper()
	value, err := release.NewCandidate(candidateSpec(t, id, generation))
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	return value
}

func artifactBytes(spec release.CandidateSpec) map[string][]byte {
	evidence, err := json.Marshal(spec.ValidationEvidence)
	if err != nil {
		panic(err)
	}
	return map[string][]byte{
		"config.json":              spec.ConfigJSON,
		"maestro-xray-cdn.service": spec.SystemdUnit,
		"rollback.json":            spec.RollbackJSON,
		"validation-report.json":   evidence,
		"xray":                     spec.XrayBinary,
	}
}
func TestCandidateManifestIsDeterministicChecksummedAndDefensivelyCopied(t *testing.T) {
	spec := candidateSpec(t, "release-a", 7)
	first, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	second, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate second: %v", err)
	}
	if !bytes.Equal(first.CanonicalManifest(), second.CanonicalManifest()) || first.ManifestSHA256() != second.ManifestSHA256() {
		t.Fatal("identical candidate inputs produced different canonical manifests")
	}
	manifest := first.Manifest()
	if manifest.SchemaVersion != 1 || manifest.ReleaseID != "release-a" || manifest.Generation != 7 ||
		manifest.TargetPort != 18081 || manifest.FallbackProbePort != 18080 {
		t.Fatalf("manifest identity/ports = %#v", manifest)
	}
	paths := make([]string, 0, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		paths = append(paths, artifact.Path)
	}
	if strings.Join(paths, ",") != "config.json,maestro-xray-cdn.service,rollback.json,validation-report.json,xray" {
		t.Fatalf("artifact order = %v", paths)
	}
	var xraySHA string
	for _, artifact := range manifest.Artifacts {
		if artifact.Path == "xray" {
			xraySHA = artifact.SHA256
		}
	}
	expectedXraySHA := sha256.Sum256(spec.XrayBinary)
	if xraySHA != hex.EncodeToString(expectedXraySHA[:]) {
		t.Fatalf("xray checksum = %q", xraySHA)
	}
	before := append([]byte(nil), first.CanonicalManifest()...)
	spec.ConfigJSON[0] ^= 0xff
	spec.XrayBinary[0] ^= 0xff
	manifest.Artifacts[0].SHA256 = strings.Repeat("0", 64)
	if !bytes.Equal(before, first.CanonicalManifest()) {
		t.Fatal("caller-owned input or returned manifest mutated the release")
	}
}

func TestManifestParserRejectsNonCanonicalUnsafeAndTamperedContent(t *testing.T) {
	candidate := mustCandidate(t, "release-a", 1)
	canonical := candidate.CanonicalManifest()
	if _, err := release.ParseManifest(canonical); err != nil {
		t.Fatalf("ParseManifest canonical: %v", err)
	}
	unknown := append(append([]byte(nil), canonical[:len(canonical)-1]...), []byte(`,"unexpected":true}`)...)
	unsafePath := bytes.Replace(canonical, []byte(`"config.json"`), []byte(`"../config.json"`), 1)
	upperHash := append([]byte(nil), canonical...)
	firstHash := candidate.Manifest().Artifacts[0].SHA256
	hashOffset := bytes.Index(upperHash, []byte(firstHash))
	if hashOffset < 0 {
		t.Fatal("artifact hash missing from canonical manifest")
	}
	for index, value := range upperHash[hashOffset : hashOffset+len(firstHash)] {
		if value >= 'a' && value <= 'f' {
			upperHash[hashOffset+index] = value - ('a' - 'A')
			break
		}
	}
	cases := map[string][]byte{
		"trailing newline":     append(append([]byte(nil), canonical...), '\n'),
		"unknown field":        unknown,
		"unsafe artifact path": unsafePath,
		"non-lowercase hash":   upperHash,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := release.ParseManifest(raw); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestArtifactVerificationRejectsMissingExtraAndDigestMismatch(t *testing.T) {
	spec := candidateSpec(t, "release-a", 1)
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	valid := artifactBytes(spec)
	if err := candidate.VerifyArtifacts(valid); err != nil {
		t.Fatalf("VerifyArtifacts valid: %v", err)
	}
	missing := artifactBytes(spec)
	delete(missing, "rollback.json")
	extra := artifactBytes(spec)
	extra["unexpected"] = []byte("extra")
	tampered := artifactBytes(spec)
	tampered["xray"] = []byte("changed")
	for name, artifacts := range map[string]map[string][]byte{"missing": missing, "extra": extra, "tampered": tampered} {
		t.Run(name, func(t *testing.T) {
			if err := candidate.VerifyArtifacts(artifacts); err == nil {
				t.Fatal("invalid artifact set accepted")
			}
		})
	}
}

func TestCatalogPromotionAndRollbackAreAtomicImmutableTransitions(t *testing.T) {
	initial := release.NewCatalog()
	withOne, err := initial.AddCandidate(mustCandidate(t, "release-1", 1))
	if err != nil {
		t.Fatalf("AddCandidate one: %v", err)
	}
	if _, exists := initial.Get("release-1"); exists {
		t.Fatal("AddCandidate mutated the original catalog")
	}
	publishedOne, err := withOne.Publish("release-1")
	if err != nil {
		t.Fatalf("Publish one: %v", err)
	}
	oneBefore, _ := withOne.Get("release-1")
	oneAfter, _ := publishedOne.Get("release-1")
	if oneBefore.State() != release.Candidate || oneAfter.State() != release.Published ||
		oneAfter.Transport().State() != controlplane.TransportReleasePublished {
		t.Fatalf("candidate/published transition = %q/%q", oneBefore.State(), oneAfter.State())
	}
	withTwo, err := publishedOne.AddCandidate(mustCandidate(t, "release-2", 2))
	if err != nil {
		t.Fatalf("AddCandidate two: %v", err)
	}
	publishedTwo, err := withTwo.Publish("release-2")
	if err != nil {
		t.Fatalf("Publish two: %v", err)
	}
	oldOne, _ := publishedTwo.Get("release-1")
	current, ok := publishedTwo.Current()
	if !ok || current.Manifest().ReleaseID != "release-2" || current.State() != release.Published || oldOne.State() != release.Retired ||
		oldOne.Transport().State() != controlplane.TransportReleaseRetired {
		t.Fatalf("atomic publish states current=%#v old=%q", current.Manifest(), oldOne.State())
	}
	withThree, _ := publishedTwo.AddCandidate(mustCandidate(t, "release-3", 3))
	publishedThree, err := withThree.Publish("release-3")
	if err != nil {
		t.Fatalf("Publish three: %v", err)
	}
	rolledBack, selected, err := publishedThree.Rollback("release-3")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	rolledCurrent, ok := rolledBack.Current()
	if !ok || selected != "release-2" || rolledCurrent.Manifest().ReleaseID != "release-2" || rolledCurrent.State() != release.Published {
		t.Fatalf("rollback selected=%q current=%#v", selected, rolledCurrent.Manifest())
	}
	stillThree, _ := publishedThree.Current()
	if stillThree.Manifest().ReleaseID != "release-3" {
		t.Fatal("Rollback mutated the original catalog")
	}
}

func TestCatalogRejectsSkippedDuplicateAndAmbiguousTransitions(t *testing.T) {
	candidate := mustCandidate(t, "release-1", 1)
	catalog, err := release.NewCatalog().AddCandidate(candidate)
	if err != nil {
		t.Fatalf("AddCandidate: %v", err)
	}
	if _, err := catalog.AddCandidate(candidate); err == nil {
		t.Fatal("duplicate release accepted")
	}
	if _, err := catalog.AddCandidate(mustCandidate(t, "release-2", 1)); err == nil {
		t.Fatal("duplicate generation accepted")
	}
	if _, _, err := catalog.Rollback("release-1"); err == nil {
		t.Fatal("candidate rollback accepted")
	}
	published, err := catalog.Publish("release-1")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := published.Publish("release-1"); err == nil {
		t.Fatal("published release published twice")
	}
	if _, _, err := published.Rollback("release-1"); err == nil {
		t.Fatal("rollback without a prior retired release accepted")
	}
}

func TestTemplatesUseIsolatedPortsAndRejectSecretLeakage(t *testing.T) {
	config := release.DefaultConfigTemplate()
	unit := release.DefaultSystemdTemplate()
	rollback := release.DefaultRollbackTemplate()
	if err := release.ValidateConfigTemplate(config); err != nil {
		t.Fatalf("ValidateConfigTemplate: %v", err)
	}
	if err := release.ValidateSystemdTemplate(unit); err != nil {
		t.Fatalf("ValidateSystemdTemplate: %v", err)
	}
	if err := release.ValidateRollbackTemplate(rollback); err != nil {
		t.Fatalf("ValidateRollbackTemplate: %v", err)
	}
	if !bytes.Contains(config, []byte(`"port":18081`)) || bytes.Contains(config, []byte("18080")) ||
		!bytes.Contains(config, []byte(`"listen":"127.0.0.1","port":18082,"protocol":"dokodemo-door"`)) ||
		!bytes.Contains(config, []byte(`"services":["StatsService"]`)) ||
		!bytes.Contains(config, []byte(`"security":"tls"`)) ||
		!bytes.Contains(config, []byte(`"verifyPeerCertInNames":["maestro-metering-client"]`)) ||
		!bytes.Contains(config, []byte(`/etc/maestro-xray-cdn/api-mtls/client-ca.crt`)) ||
		!bytes.Contains(unit, []byte(`ReadOnlyPaths=/etc/maestro-xray-cdn/api-mtls`)) ||
		!bytes.Contains(config, []byte(`"access":"none"`)) ||
		!bytes.Contains(config, []byte(`/var/log/maestro-xray-cdn/error.log`)) ||
		!bytes.Contains(unit, []byte("maestro-xray-cdn.service")) || !bytes.Contains(unit, []byte(release.RuntimeConfigPath)) || bytes.Contains(unit, []byte("/current/config.json")) || bytes.Contains(unit, []byte("18080")) ||
		!bytes.Contains(unit, []byte("LogsDirectory=maestro-xray-cdn")) ||
		!bytes.Contains(unit, []byte("LogsDirectoryMode=0750")) ||
		!bytes.Contains(rollback, []byte(`"fallback_probe_port":18080`)) {
		t.Fatalf("isolated api/log/fallback template contract missing")
	}
	secret := "synthetic-server-decryption-material"
	leakedConfig := bytes.Replace(config, []byte("<RUNTIME_SERVER_DECRYPTION>"), []byte(secret), 1)
	leakedUnit := append(append([]byte(nil), unit...), []byte("\nEnvironment=API_TOKEN="+secret+"\n")...)
	badRollback := bytes.Replace(rollback, []byte("18080"), []byte("18081"), 1)
	for name, validate := range map[string]func() error{
		"config secret":    func() error { return release.ValidateConfigTemplate(leakedConfig) },
		"unit secret":      func() error { return release.ValidateSystemdTemplate(leakedUnit) },
		"fallback changed": func() error { return release.ValidateRollbackTemplate(badRollback) },
	} {
		t.Run(name, func(t *testing.T) {
			err := validate()
			if err == nil {
				t.Fatal("unsafe template accepted")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("validation error leaked secret value")
			}
		})
	}
}

func TestAPIControlBoundaryRejectsUnauthenticatedConfiguration(t *testing.T) {
	config := release.DefaultConfigTemplate()
	withoutTLS := bytes.Replace(config, []byte(`"security":"tls"`), []byte(`"security":"none"`), 1)
	withoutClientIdentity := bytes.Replace(
		config,
		[]byte(`"verifyPeerCertInNames":["maestro-metering-client"]`),
		[]byte(`"verifyPeerCertInNames":[]`),
		1,
	)
	withoutClientCA := bytes.Replace(config, []byte(`,"usage":"verify"`), []byte{}, 1)
	for name, raw := range map[string][]byte{
		"tls disabled":                   withoutTLS,
		"client identity missing":        withoutClientIdentity,
		"client CA verification missing": withoutClientCA,
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(raw, config) {
				t.Fatal("negative fixture did not change the config")
			}
			if err := release.ValidateConfigTemplate(raw); err == nil {
				t.Fatal("unauthenticated metering boundary accepted")
			}
		})
	}
}

func TestCatalogRejectsStaleCandidatePromotion(t *testing.T) {
	catalog, err := release.NewCatalog().AddCandidate(mustCandidate(t, "release-2", 2))
	if err != nil {
		t.Fatalf("AddCandidate current: %v", err)
	}
	published, err := catalog.Publish("release-2")
	if err != nil {
		t.Fatalf("Publish current: %v", err)
	}
	withStale, err := published.AddCandidate(mustCandidate(t, "release-1", 1))
	if err != nil {
		t.Fatalf("AddCandidate stale: %v", err)
	}
	if _, err := withStale.Publish("release-1"); err == nil {
		t.Fatal("stale candidate replaced a newer published release")
	}
	current, ok := withStale.Current()
	if !ok || current.Manifest().ReleaseID != "release-2" {
		t.Fatal("rejected stale publication mutated the catalog")
	}
}

func TestManifestRejectsArtifactSizesBeyondPathLimits(t *testing.T) {
	candidate := mustCandidate(t, "release-a", 1)
	limits := map[string]int64{
		"config.json":              1 << 20,
		"maestro-xray-cdn.service": 64 << 10,
		"rollback.json":            64 << 10,
		"validation-report.json":   64 << 10,
		"xray":                     256 << 20,
	}
	for path, limit := range limits {
		t.Run(path, func(t *testing.T) {
			manifest := candidate.Manifest()
			for index := range manifest.Artifacts {
				if manifest.Artifacts[index].Path == path {
					manifest.Artifacts[index].Size = limit + 1
				}
			}
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if _, err := release.ParseManifest(raw); err == nil {
				t.Fatal("oversized artifact declaration accepted")
			}
		})
	}
}

func TestReleaseDirectoryValidationRejectsUnexpectedAndSymlinkArtifacts(t *testing.T) {
	spec := candidateSpec(t, "release-a", 1)
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	write := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
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
			t.Fatalf("seal release dir: %v", err)
		}
		return dir
	}
	t.Run("valid", func(t *testing.T) {
		if err := release.ValidateReleaseDirectory(write(t)); err != nil {
			t.Fatalf("ValidateReleaseDirectory: %v", err)
		}
	})
	t.Run("unexpected", func(t *testing.T) {
		dir := write(t)
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("unseal fixture dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "unexpected"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write unexpected: %v", err)
		}
		if err := release.ValidateReleaseDirectory(dir); err == nil {
			t.Fatal("unexpected release file accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink semantics are covered by Linux CI")
		}
		dir := write(t)
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("unseal fixture dir: %v", err)
		}
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("abc"), 0o600); err != nil {
			t.Fatalf("write outside fixture: %v", err)
		}
		if err := os.Remove(filepath.Join(dir, "xray")); err != nil {
			t.Fatalf("remove xray fixture: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "xray")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if err := release.ValidateReleaseDirectory(dir); err == nil {
			t.Fatal("symlink artifact accepted")
		}
	})
	t.Run("non-executable xray", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX mode contract is covered by Linux CI")
		}
		dir := write(t)
		if err := os.Chmod(filepath.Join(dir, "xray"), 0o600); err != nil {
			t.Fatalf("chmod xray: %v", err)
		}
		if err := release.ValidateReleaseDirectory(dir); err == nil {
			t.Fatal("non-executable xray accepted")
		}
	})
	t.Run("group-writable config", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX mode contract is covered by Linux CI")
		}
		dir := write(t)
		if err := os.Chmod(filepath.Join(dir, "config.json"), 0o620); err != nil {
			t.Fatalf("chmod config: %v", err)
		}
		if err := release.ValidateReleaseDirectory(dir); err == nil {
			t.Fatal("group-writable config accepted")
		}
	})
	t.Run("world-writable manifest", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX mode contract is covered by Linux CI")
		}
		dir := write(t)
		if err := os.Chmod(filepath.Join(dir, "manifest.json"), 0o602); err != nil {
			t.Fatalf("chmod manifest: %v", err)
		}
		if err := release.ValidateReleaseDirectory(dir); err == nil {
			t.Fatal("world-writable manifest accepted")
		}
	})
	t.Run("special mode bits", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX mode contract is covered by Linux CI")
		}
		for name, target := range map[string]struct {
			path func(string) string
			mode os.FileMode
		}{
			"setuid xray":        {path: func(dir string) string { return filepath.Join(dir, "xray") }, mode: 0o700 | os.ModeSetuid},
			"setgid xray":        {path: func(dir string) string { return filepath.Join(dir, "xray") }, mode: 0o700 | os.ModeSetgid},
			"sticky release dir": {path: func(dir string) string { return dir }, mode: 0o700 | os.ModeSticky},
		} {
			t.Run(name, func(t *testing.T) {
				dir := write(t)
				if err := os.Chmod(target.path(dir), target.mode); err != nil {
					t.Fatalf("chmod special mode: %v", err)
				}
				if err := release.ValidateReleaseDirectory(dir); err == nil {
					t.Fatal("release with special mode bit accepted")
				}
			})
		}
	})
}

func TestCandidateRejectsUnpinnedProvenanceAndUnsafeReleaseIdentity(t *testing.T) {
	base := candidateSpec(t, "release-a", 1)
	cases := map[string]func(*release.CandidateSpec){
		"unsafe id": func(spec *release.CandidateSpec) {
			transport, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
				ID: "../release", Profile: spec.Transport.Profile(), Preset: spec.Transport.Preset(),
				State: controlplane.TransportReleaseCandidate, ApprovedEdges: spec.Transport.ApprovedEdges(),
			})
			if err == nil {
				spec.Transport = transport
			} else {
				spec.XrayVersion = "../release"
			}
		},
		"floating version": func(spec *release.CandidateSpec) { spec.XrayVersion = "latest" },
		"short commit":     func(spec *release.CandidateSpec) { spec.XrayCommit = "abc" },
		"empty binary":     func(spec *release.CandidateSpec) { spec.XrayBinary = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := base
			spec.XrayBinary = append([]byte(nil), base.XrayBinary...)
			spec.ConfigJSON = append([]byte(nil), base.ConfigJSON...)
			spec.SystemdUnit = append([]byte(nil), base.SystemdUnit...)
			spec.RollbackJSON = append([]byte(nil), base.RollbackJSON...)
			mutate(&spec)
			if _, err := release.NewCandidate(spec); err == nil {
				t.Fatal("unsafe candidate accepted")
			}
		})
	}
}

func TestManifestDigestMatchesCanonicalBytes(t *testing.T) {
	candidate := mustCandidate(t, "release-a", 1)
	digest := sha256.Sum256(candidate.CanonicalManifest())
	if candidate.ManifestSHA256() != hex.EncodeToString(digest[:]) {
		t.Fatal("manifest digest is not bound to canonical manifest bytes")
	}
}
