package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/backuprpo"
)

func validCapabilityBindings() capabilityBindings {
	return capabilityBindings{
		RQLiteCASHA256:     strings.Repeat("a", 64),
		RQLiteCertSHA256:   strings.Repeat("b", 64),
		RQLiteKeySHA256:    strings.Repeat("c", 64),
		VerifyScriptSHA256: strings.Repeat("d", 64),
		GPGSHA256:          strings.Repeat("e", 64),
		PythonSHA256:       strings.Repeat("f", 64),
	}
}

func validCapabilityEvidence(config workerConfig, bindings capabilityBindings) capabilityEvidence {
	return capabilityEvidence{
		Version:              2,
		Generation:           17,
		IssuedAtUnix:         2_000_000_000,
		ExpiresAtUnix:        2_000_000_120,
		RQLiteEndpoints:      append([]string(nil), config.RQLiteEndpoints...),
		RQLiteCASHA256:       bindings.RQLiteCASHA256,
		RQLiteCertSHA256:     bindings.RQLiteCertSHA256,
		RQLiteKeySHA256:      bindings.RQLiteKeySHA256,
		YandexEndpoint:       config.YandexEndpoint,
		YandexRegion:         config.YandexRegion,
		YandexBucket:         config.YandexBucket,
		YandexPrefix:         config.YandexPrefix,
		ObjectProbeKey:       config.YandexPrefix + "/.maestro-capability/read-write-probe",
		ObjectProbeVersionID: "probe-version-0001",
		ObjectProbeSHA256:    strings.Repeat("1", 64),
		ObjectProbeSizeBytes: 32,
		SignerFingerprint:    config.SignerFingerprint,
		RecipientFingerprint: config.RecipientFingerprint,
		VerifyScriptSHA256:   bindings.VerifyScriptSHA256,
		GPGSHA256:            bindings.GPGSHA256,
		PythonSHA256:         bindings.PythonSHA256,
	}
}

func TestDecodeCapabilityEvidenceRejectsLegacyOpaqueSHA(t *testing.T) {
	raw := `{"version":1,"generation":9,"sha256":"` + strings.Repeat("a", 64) + `"}`
	if _, err := decodeCapabilityEvidence(strings.NewReader(raw)); !errors.Is(err, errConfig) {
		t.Fatalf("error = %v, want errConfig", err)
	}
}

func TestValidateCapabilityEvidenceBindsRuntimeAndComputesDigest(t *testing.T) {
	config := decodeValidConfig(t)
	bindings := validCapabilityBindings()
	evidence := validCapabilityEvidence(config, bindings)

	material, err := validateCapabilityEvidence(config, evidence, bindings)
	if err != nil {
		t.Fatalf("validate evidence: %v", err)
	}
	if material.Generation != evidence.Generation ||
		material.IssuedAtUnix != evidence.IssuedAtUnix ||
		material.ExpiresAtUnix != evidence.ExpiresAtUnix {
		t.Fatalf("material timing/generation = %+v, want immutable evidence values", material)
	}
	if material.Probe.Key != evidence.ObjectProbeKey ||
		material.Probe.VersionID.String() != evidence.ObjectProbeVersionID ||
		material.Probe.SHA256 != evidence.ObjectProbeSHA256 ||
		material.Probe.SizeBytes != evidence.ObjectProbeSizeBytes {
		t.Fatalf("probe = %+v, want exact evidence probe", material.Probe)
	}
	canonical, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256(canonical))
	if material.EvidenceSHA256 != wantDigest {
		t.Fatalf("digest = %q, want canonical %q", material.EvidenceSHA256, wantDigest)
	}
}

func TestValidateCapabilityEvidenceRejectsAnyRuntimeBindingMismatch(t *testing.T) {
	config := decodeValidConfig(t)
	bindings := validCapabilityBindings()
	tests := map[string]func(*capabilityEvidence){
		"legacy version":       func(e *capabilityEvidence) { e.Version = 1 },
		"rqlite endpoints":     func(e *capabilityEvidence) { e.RQLiteEndpoints[0] = "https://127.0.0.2:4001" },
		"rqlite ca":            func(e *capabilityEvidence) { e.RQLiteCASHA256 = strings.Repeat("0", 64) },
		"rqlite cert":          func(e *capabilityEvidence) { e.RQLiteCertSHA256 = strings.Repeat("0", 64) },
		"rqlite key":           func(e *capabilityEvidence) { e.RQLiteKeySHA256 = strings.Repeat("0", 64) },
		"yandex endpoint":      func(e *capabilityEvidence) { e.YandexEndpoint = "https://example.invalid" },
		"yandex region":        func(e *capabilityEvidence) { e.YandexRegion = "other" },
		"yandex bucket":        func(e *capabilityEvidence) { e.YandexBucket = "other-bucket" },
		"yandex prefix":        func(e *capabilityEvidence) { e.YandexPrefix = "other-prefix" },
		"probe key":            func(e *capabilityEvidence) { e.ObjectProbeKey += "-other" },
		"probe version":        func(e *capabilityEvidence) { e.ObjectProbeVersionID = "" },
		"malformed probe sha":  func(e *capabilityEvidence) { e.ObjectProbeSHA256 = "ABC" },
		"probe size":           func(e *capabilityEvidence) { e.ObjectProbeSizeBytes = 0 },
		"signer":               func(e *capabilityEvidence) { e.SignerFingerprint = strings.Repeat("A", 40) },
		"recipient":            func(e *capabilityEvidence) { e.RecipientFingerprint = strings.Repeat("B", 40) },
		"verify script binary": func(e *capabilityEvidence) { e.VerifyScriptSHA256 = strings.Repeat("0", 64) },
		"gpg binary":           func(e *capabilityEvidence) { e.GPGSHA256 = strings.Repeat("0", 64) },
		"python binary":        func(e *capabilityEvidence) { e.PythonSHA256 = strings.Repeat("0", 64) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			evidence := validCapabilityEvidence(config, bindings)
			mutate(&evidence)
			if _, err := validateCapabilityEvidence(config, evidence, bindings); !errors.Is(err, errConfig) {
				t.Fatalf("error = %v, want errConfig", err)
			}
		})
	}
}

type fakeLocalReadiness struct {
	name  string
	calls *[]string
	err   error
}

func (check fakeLocalReadiness) CheckReadiness(context.Context) error {
	*check.calls = append(*check.calls, check.name)
	return check.err
}

type fakeObjectReadiness struct {
	calls *[]string
	probe backuprpo.ObjectReadinessProbe
	err   error
}

func (check *fakeObjectReadiness) CheckReadWriteReadiness(_ context.Context, probe backuprpo.ObjectReadinessProbe) error {
	*check.calls = append(*check.calls, "object")
	check.probe = probe
	return check.err
}

func TestProductionCapabilityGateChecksAllReadinessBeforeLease(t *testing.T) {
	version, err := backuprpo.NewVersionID("probe-version-0001")
	if err != nil {
		t.Fatalf("version id: %v", err)
	}
	probe := backuprpo.ObjectReadinessProbe{Key: "control-plane/.maestro-capability/read-write-probe", VersionID: version, SHA256: strings.Repeat("1", 64), SizeBytes: 32}
	calls := []string{}
	object := &fakeObjectReadiness{calls: &calls}
	gate := productionCapabilityGate{
		rqlite:  fakeLocalReadiness{name: "rqlite", calls: &calls},
		objects: object,
		crypto:  fakeLocalReadiness{name: "crypto", calls: &calls},
		probe:   probe,
	}
	if err := gate.Check(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}
	if got := strings.Join(calls, ","); got != "rqlite,object,crypto" {
		t.Fatalf("calls = %q, want rqlite,object,crypto", got)
	}
	if object.probe != probe {
		t.Fatalf("probe = %+v, want %+v", object.probe, probe)
	}
}

func TestProductionCapabilityGateFailsClosedAndStops(t *testing.T) {
	calls := []string{}
	gate := productionCapabilityGate{
		rqlite:  fakeLocalReadiness{name: "rqlite", calls: &calls},
		objects: &fakeObjectReadiness{calls: &calls, err: errors.New("sensitive provider detail")},
		crypto:  fakeLocalReadiness{name: "crypto", calls: &calls},
	}
	if err := gate.Check(context.Background()); !errors.Is(err, errOperational) {
		t.Fatalf("error = %v, want redacted errOperational", err)
	}
	if got := strings.Join(calls, ","); got != "rqlite,object" {
		t.Fatalf("calls = %q, want fail-fast rqlite,object", got)
	}
}
