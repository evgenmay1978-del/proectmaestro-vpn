package release_test

import (
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func task7ReportIndex(t *testing.T, reports []release.GateReport, gateID string) int {
	t.Helper()
	for index := range reports {
		if reports[index].GateID == gateID {
			return index
		}
	}
	t.Fatalf("gate report %q not found", gateID)
	return -1
}

func TestEvidenceClassMinimumsAreExplicit(t *testing.T) {
	expected := map[string]release.EvidenceClass{
		"billing_identity":        release.EvidenceFixtureReplay,
		"client_import":           release.EvidenceDeviceObserved,
		"config_validation":       release.EvidenceSchemaOnly,
		"direct_origin":           release.EvidenceIsolatedRealBinary,
		"isolated_start":          release.EvidenceIsolatedRealBinary,
		"literal_edge":            release.EvidenceIsolatedRealBinary,
		"local_vless":             release.EvidenceIsolatedRealBinary,
		"per_user_stats":          release.EvidenceIsolatedRealBinary,
		"production_baseline":     release.EvidenceProductionObserved,
		"subscription_regression": release.EvidenceFixtureReplay,
		"xray_config_test":        release.EvidenceIsolatedRealBinary,
		"yandex_get_body":         release.EvidenceIsolatedRealBinary,
	}

	for gateID, want := range expected {
		got, ok := release.MinimumEvidenceClass(gateID)
		if !ok || got != want {
			t.Fatalf("MinimumEvidenceClass(%q) = %q, %t; want %q, true", gateID, got, ok, want)
		}
	}
	if _, ok := release.MinimumEvidenceClass("future_gate"); ok {
		t.Fatal("unknown gate received an evidence minimum")
	}
}

func TestEvidenceClassRejectsDowngradedSignedGate(t *testing.T) {
	cases := map[string]release.EvidenceClass{
		"direct_origin":       release.EvidenceFixtureReplay,
		"isolated_start":      release.EvidenceFixtureReplay,
		"literal_edge":        release.EvidenceFixtureReplay,
		"local_vless":         release.EvidenceFixtureReplay,
		"per_user_stats":      release.EvidenceFixtureReplay,
		"xray_config_test":    release.EvidenceFixtureReplay,
		"yandex_get_body":     release.EvidenceFixtureReplay,
		"client_import":       release.EvidenceIsolatedRealBinary,
		"production_baseline": release.EvidenceDeviceObserved,
	}

	for gateID, downgraded := range cases {
		t.Run(gateID, func(t *testing.T) {
			spec, _, privateKey := taskASpec(t, "release-task-7-downgrade")
			reports := taskASignedReports(t, spec, privateKey, time.Now().UTC())
			index := task7ReportIndex(t, reports, gateID)
			reports[index].EvidenceClass = downgraded
			taskAResignReport(t, &reports[index], privateKey)

			if _, err := release.BuildValidationEvidence(spec, reports); err == nil {
				t.Fatalf("%s accepted downgraded class %s", gateID, downgraded)
			}
		})
	}
}

func TestEvidenceClassAcceptsExactMinimumAndStrongerEvidence(t *testing.T) {
	spec, _, privateKey := taskASpec(t, "release-task-7-minimum")
	reports := taskASignedReports(t, spec, privateKey, time.Now().UTC())
	if _, err := release.BuildValidationEvidence(spec, reports); err != nil {
		t.Fatalf("exact minimum evidence rejected: %v", err)
	}

	for index := range reports {
		reports[index].EvidenceClass = release.EvidenceProductionObserved
		taskAResignReport(t, &reports[index], privateKey)
	}
	if _, err := release.BuildValidationEvidence(spec, reports); err != nil {
		t.Fatalf("stronger evidence rejected: %v", err)
	}
}

func TestEvidenceClassRejectsMissingUnknownAndPostSignatureChange(t *testing.T) {
	for name, mutate := range map[string]func(*release.GateReport){
		"missing": func(report *release.GateReport) {
			report.EvidenceClass = ""
		},
		"unknown": func(report *release.GateReport) {
			report.EvidenceClass = release.EvidenceClass("FUTURE_EVIDENCE")
		},
		"mixed case": func(report *release.GateReport) {
			report.EvidenceClass = release.EvidenceClass("Fixture_Replay")
		},
		"changed after signing": func(report *release.GateReport) {
			report.EvidenceClass = release.EvidenceProductionObserved
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec, _, privateKey := taskASpec(t, "release-task-7-malformed-class")
			reports := taskASignedReports(t, spec, privateKey, time.Now().UTC())
			mutate(&reports[0])
			if _, err := release.BuildValidationEvidence(spec, reports); err == nil {
				t.Fatal("invalid or unsigned evidence-class mutation accepted")
			}
		})
	}
}
