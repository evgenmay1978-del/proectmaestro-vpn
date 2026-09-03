package release_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func transportForProfile(t *testing.T, id, profileID string) controlplane.TransportRelease {
	t.Helper()
	presetID := "preset-" + profileID
	profile := controlplane.TransportProfile{
		ID: profileID, PublicHost: "cdn.example.invalid",
		SecretPath: "/static/test/segment.ts/opaque", OriginRouteID: "origin-" + profileID,
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

func candidateForProfile(t *testing.T, id, profileID string, generation uint64) release.Release {
	t.Helper()
	spec, _, privateKey := taskASpec(t, id)
	spec.Generation = generation
	spec.Transport = transportForProfile(t, id, profileID)
	evidence, err := release.BuildValidationEvidence(spec, taskASignedReports(t, spec, privateKey, time.Now().UTC()))
	if err != nil {
		t.Fatalf("BuildValidationEvidence: %v", err)
	}
	spec.ValidationEvidence = evidence
	value, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	return value
}

func TestCatalogTransitionsNeverCrossTransportProfiles(t *testing.T) {
	catalog := release.NewCatalog()
	var err error
	for _, candidate := range []release.Release{
		candidateForProfile(t, "profile-a-1", "profile-a", 1),
		candidateForProfile(t, "profile-b-1", "profile-b", 2),
		candidateForProfile(t, "profile-a-2", "profile-a", 3),
	} {
		catalog, err = catalog.AddCandidate(candidate)
		if err != nil {
			t.Fatalf("AddCandidate: %v", err)
		}
	}
	catalog, err = catalog.Publish("profile-a-1")
	if err != nil {
		t.Fatalf("Publish profile A: %v", err)
	}
	catalog, err = catalog.Publish("profile-b-1")
	if err != nil {
		t.Fatalf("Publish profile B: %v", err)
	}
	a, _ := catalog.Get("profile-a-1")
	b, _ := catalog.Get("profile-b-1")
	if a.State() != release.Published || b.State() != release.Published {
		t.Fatalf("publishing profile B changed profile A: A=%s B=%s", a.State(), b.State())
	}
	catalog, err = catalog.Publish("profile-a-2")
	if err != nil {
		t.Fatalf("Publish profile A replacement: %v", err)
	}
	b, _ = catalog.Get("profile-b-1")
	if b.State() != release.Published {
		t.Fatalf("publishing profile A replacement changed profile B: %s", b.State())
	}
	rolled, selected, err := catalog.Rollback("profile-a-2")
	if err != nil {
		t.Fatalf("Rollback profile A: %v", err)
	}
	if selected != "profile-a-1" {
		t.Fatalf("cross-profile rollback selected %q", selected)
	}
	b, _ = rolled.Get("profile-b-1")
	if b.State() != release.Published {
		t.Fatalf("profile A rollback changed profile B: %s", b.State())
	}
}

func TestRuntimeMaterializationIsSeparateDeterministicAndSealed(t *testing.T) {
	candidate := mustCandidate(t, "release-a", 1)
	templateBefore := candidateSpec(t, "release-a", 1).ConfigJSON
	material := taskARuntimeMaterialValue(taskARuntimeMaterial)
	first, err := candidate.MaterializeRuntimeConfig(material)
	if err != nil {
		t.Fatalf("MaterializeRuntimeConfig: %v", err)
	}
	second, err := candidate.MaterializeRuntimeConfig(material)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("runtime materialization is not deterministic")
	}
	if _, err := candidate.MaterializeRuntimeConfig(taskARuntimeMaterialValue("different-material")); err == nil {
		t.Fatal("runtime materialization accepted uncommitted material")
	}
	profile := candidate.Transport().Profile()
	if bytes.Contains(first, []byte("<RUNTIME_")) || !bytes.Contains(first, []byte(profile.PublicHost)) ||
		!bytes.Contains(first, []byte(profile.SecretPath)) || bytes.Contains(first, []byte("runtime.example.invalid")) {
		t.Fatal("runtime config contains unresolved placeholders or missed material")
	}
	if !bytes.Contains(release.DefaultSystemdTemplate(), []byte("/run/maestro-xray-cdn/config.json")) ||
		bytes.Contains(release.DefaultSystemdTemplate(), []byte("/current/config.json")) {
		t.Fatal("systemd does not consume the validated runtime config")
	}
	if !bytes.Equal(templateBefore, candidateSpec(t, "release-a", 1).ConfigJSON) {
		t.Fatal("materialization mutated the immutable template")
	}
}

func TestCandidateRequiresPinnedBinaryAndDigestBoundGateEvidence(t *testing.T) {
	typ := reflect.TypeOf(release.CandidateSpec{})
	for _, field := range []string{"XraySource", "XrayBinarySHA256", "ValidationEvidence"} {
		if _, ok := typ.FieldByName(field); !ok {
			t.Fatalf("CandidateSpec missing %s", field)
		}
	}
	spec := candidateSpec(t, "release-a", 1)
	spec.XrayBinary = []byte("abc")
	if _, err := release.NewCandidate(spec); err == nil {
		t.Fatal("arbitrary unproven binary accepted")
	}
}

func TestDefaultConfigDisablesDestinationAccessLogsAndScopesMutationAPI(t *testing.T) {
	config := release.DefaultConfigTemplate()
	if bytes.Contains(config, []byte("/access.log")) || !bytes.Contains(config, []byte(`"access":"none"`)) {
		t.Fatal("destination-bearing access logging is enabled")
	}
	if !bytes.Contains(config, []byte("HandlerService")) || !bytes.Contains(config, []byte("StatsService")) {
		t.Fatal("isolated Xray API does not expose the exact metering and sidecar services")
	}
	if !bytes.Contains(config, []byte("maestro-metering-client")) || !bytes.Contains(config, []byte("maestro-sidecar-agent")) {
		t.Fatal("isolated Xray API does not pin both dedicated mTLS identities")
	}
}

func TestCatalogJournalPreservesDraftAndRollbackStateAcrossRestart(t *testing.T) {
	type journaledCatalog interface {
		AddDraft(release.CandidateSpec) (release.Catalog, error)
		PromoteDraft(string) (release.Catalog, error)
		Snapshot(release.LifecycleSigner) ([]byte, error)
		Restore([]byte, []release.Release, release.LifecycleTrust, uint64) (release.Catalog, error)
	}
	catalog := release.NewCatalog()
	journaled, ok := any(catalog).(journaledCatalog)
	if !ok {
		t.Fatal("catalog has no durable lifecycle journal seam")
	}
	drafted, err := journaled.AddDraft(candidateSpec(t, "release-a", 1))
	if err != nil {
		t.Fatalf("AddDraft: %v", err)
	}
	draft, _ := drafted.Get("release-a")
	if draft.State() != release.State("DRAFT") {
		t.Fatalf("draft state = %q", draft.State())
	}
	candidateCatalog, err := any(drafted).(journaledCatalog).PromoteDraft("release-a")
	if err != nil {
		t.Fatalf("PromoteDraft: %v", err)
	}
	published, err := candidateCatalog.Publish("release-a")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	signer, trust, _ := lifecycleCredentials(t)
	snapshot, err := any(published).(journaledCatalog).Snapshot(signer)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	journalPath := filepath.Join(t.TempDir(), "release-journal.json")
	if err := os.WriteFile(journalPath, snapshot, 0o600); err != nil {
		t.Fatalf("write journal fixture: %v", err)
	}
	restoredBytes, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read journal fixture: %v", err)
	}
	restored, err := any(release.NewCatalog()).(journaledCatalog).Restore(restoredBytes, []release.Release{draft}, trust, 1)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	current, ok := restored.Get("release-a")
	if !ok || current.State() != release.Published {
		t.Fatalf("restored release state = %q exists=%t", current.State(), ok)
	}
}

func TestSealedDirectoryRejectsWritableHardlinkedAndSymlinkAncestorArtifacts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("POSIX inode and mode contract is covered by Linux CI")
	}
	spec := candidateSpec(t, "release-a", 1)
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	write := func(t *testing.T, parent string) string {
		t.Helper()
		dir := filepath.Join(parent, "release")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
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
			t.Fatalf("seal dir: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Errorf("restore release dir permissions: %v", err)
			}
		})
		return dir
	}
	t.Run("sealed valid", func(t *testing.T) {
		if err := release.ValidateReleaseDirectoryWithTrust(write(t, t.TempDir()), spec.EvidenceTrust); err != nil {
			t.Fatalf("ValidateReleaseDirectory: %v", err)
		}
	})
	t.Run("owner writable", func(t *testing.T) {
		dir := write(t, t.TempDir())
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("chmod dir: %v", err)
		}
		if err := os.Chmod(filepath.Join(dir, "config.json"), 0o600); err != nil {
			t.Fatalf("chmod config: %v", err)
		}
		if err := release.ValidateReleaseDirectoryWithTrust(dir, spec.EvidenceTrust); err == nil {
			t.Fatal("owner-writable published artifact accepted")
		}
	})
	t.Run("hardlink", func(t *testing.T) {
		dir := write(t, t.TempDir())
		if err := os.Link(filepath.Join(dir, "xray"), filepath.Join(t.TempDir(), "linked-xray")); err != nil {
			t.Fatalf("hardlink: %v", err)
		}
		if err := release.ValidateReleaseDirectoryWithTrust(dir, spec.EvidenceTrust); err == nil {
			t.Fatal("hardlinked published artifact accepted")
		}
	})
	t.Run("symlink ancestor", func(t *testing.T) {
		realParent := t.TempDir()
		dir := write(t, realParent)
		linkParent := filepath.Join(t.TempDir(), "linked-parent")
		if err := os.Symlink(realParent, linkParent); err != nil {
			t.Fatalf("symlink parent: %v", err)
		}
		linkedDir := filepath.Join(linkParent, filepath.Base(dir))
		if err := release.ValidateReleaseDirectoryWithTrust(linkedDir, spec.EvidenceTrust); err == nil {
			t.Fatal("release below symlink ancestor accepted")
		}
	})
}

func TestDirectoryEvidenceIsBoundToManifestAndBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sealed filesystem contract is covered by Linux CI")
	}
	spec := candidateSpec(t, "release-a", 1)
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	manifest := candidate.Manifest()
	manifest.CandidateSHA256 = strings.Repeat("c", 64)
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal manifest: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestJSON, 0o400); err != nil {
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
		t.Fatalf("seal dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore release dir permissions: %v", err)
		}
	})
	if err := release.ValidateReleaseDirectoryWithTrust(dir, spec.EvidenceTrust); err == nil {
		t.Fatal("validation evidence detached from manifest candidate digest")
	}
}
func TestValidationErrorsExposeStableNonSecretReasonCodes(t *testing.T) {
	type reasoned interface{ ReasonCode() string }
	err := release.ValidateReleaseDirectory("")
	value, ok := err.(reasoned)
	if !ok || value.ReasonCode() == "" {
		t.Fatal("validation error has no stable reason code")
	}
	if strings.Contains(value.ReasonCode(), string(os.PathSeparator)) || strings.Contains(value.ReasonCode(), " ") {
		t.Fatalf("reason code is not stable/non-secret: %q", value.ReasonCode())
	}
}
