package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
)

func validWhiteListPublicationFacts() WhiteListPublicationFacts {
	return WhiteListPublicationFacts{
		NowUnix:                 2_100_000_000,
		ActivationSource:        WhiteListActivationConfirmedGBPurchase,
		ActivationEntitlementID: "wl-ent-11111111111111111111111111111111",
		EntitlementID:           "wl-ent-11111111111111111111111111111111",
		EntitlementState:        EntitlementActive,
		PrimaryStatus:           "active",
		PrimaryExpiresAtUnix:    2_100_000_100,
		ProjectionVersion:       7,
		AvailableBytes:          1_000_000_000,
		ObservedThroughUnix:     2_099_999_998,
		ReleaseBindingExact:     true,
		CredentialUsable:        true,
		DesiredGeneration:       9,
		ReceiptSetReady:         true,
		ReceiptsFreshUntilUnix:  2_100_000_010,
		ApprovedNodeCount:       2,
	}
}

func TestEvaluateWhiteListPublicationAllowsOnlyPurchasedOrAdminEnabledBalance(t *testing.T) {
	for _, source := range []WhiteListActivationSource{
		WhiteListActivationConfirmedGBPurchase,
		WhiteListActivationAdminEnable,
	} {
		facts := validWhiteListPublicationFacts()
		facts.ActivationSource = source
		decision := EvaluateWhiteListPublication(facts)
		if decision.Verdict != WhiteListPublicationPublishable ||
			decision.ProjectionVersion != facts.ProjectionVersion ||
			decision.DesiredGeneration != facts.DesiredGeneration ||
			decision.FreshUntilUnix != facts.ObservedThroughUnix+5 {
			t.Fatalf("source=%q decision=%#v", source, decision)
		}
	}

	for _, source := range []WhiteListActivationSource{
		WhiteListActivationDisabled,
		WhiteListActivationSource("PAYMENT_CLAIMED"),
		WhiteListActivationSource("GENERIC_PURCHASE"),
	} {
		facts := validWhiteListPublicationFacts()
		facts.ActivationSource = source
		decision := EvaluateWhiteListPublication(facts)
		if decision != (WhiteListPublicationDecision{Verdict: WhiteListPublicationNoEntitlement}) {
			t.Fatalf("source=%q decision=%#v, want closed", source, decision)
		}
	}
}

func TestEvaluateWhiteListPublicationUsesStrictFailClosedOrder(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*WhiteListPublicationFacts)
		verdict WhiteListPublicationVerdict
	}{
		{"activation entitlement mismatch", func(f *WhiteListPublicationFacts) { f.ActivationEntitlementID += "-other" }, WhiteListPublicationNoEntitlement},
		{"inactive entitlement", func(f *WhiteListPublicationFacts) { f.EntitlementState = EntitlementSuspended }, WhiteListPublicationNoEntitlement},
		{"primary inactive", func(f *WhiteListPublicationFacts) { f.PrimaryStatus = "suspended" }, WhiteListPublicationPrimaryExpired},
		{"primary expiry boundary", func(f *WhiteListPublicationFacts) { f.PrimaryExpiresAtUnix = f.NowUnix }, WhiteListPublicationPrimaryExpired},
		{"projection missing", func(f *WhiteListPublicationFacts) { f.ProjectionVersion = 0 }, WhiteListPublicationProjectionPending},
		{"projection pending", func(f *WhiteListPublicationFacts) { f.ProjectionPending = true }, WhiteListPublicationProjectionPending},
		{"projection freshness boundary", func(f *WhiteListPublicationFacts) { f.ObservedThroughUnix = f.NowUnix - 5 }, WhiteListPublicationProjectionStale},
		{"no balance", func(f *WhiteListPublicationFacts) { f.AvailableBytes = 0 }, WhiteListPublicationNoBalance},
		{"release mismatch", func(f *WhiteListPublicationFacts) { f.ReleaseBindingExact = false }, WhiteListPublicationReleaseMismatch},
		{"credential unusable", func(f *WhiteListPublicationFacts) { f.CredentialUsable = false }, WhiteListPublicationSidecarUnavailable},
		{"generation missing", func(f *WhiteListPublicationFacts) { f.DesiredGeneration = 0 }, WhiteListPublicationSidecarUnavailable},
		{"receipts missing", func(f *WhiteListPublicationFacts) { f.ReceiptSetReady = false }, WhiteListPublicationSidecarUnavailable},
		{"receipts expiry boundary", func(f *WhiteListPublicationFacts) { f.ReceiptsFreshUntilUnix = f.NowUnix }, WhiteListPublicationSidecarUnavailable},
		{"approved nodes missing", func(f *WhiteListPublicationFacts) { f.ApprovedNodeCount = 0 }, WhiteListPublicationSidecarUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := validWhiteListPublicationFacts()
			test.mutate(&facts)
			decision := EvaluateWhiteListPublication(facts)
			if decision != (WhiteListPublicationDecision{Verdict: test.verdict}) {
				t.Fatalf("decision=%#v, want closed verdict %q and zero metadata", decision, test.verdict)
			}
		})
	}
}

func TestEvaluateWhiteListPublicationPrecedenceAndFreshnessMinimum(t *testing.T) {
	facts := validWhiteListPublicationFacts()
	facts.ActivationSource = WhiteListActivationDisabled
	facts.PrimaryStatus = "suspended"
	facts.ProjectionPending = true
	facts.AvailableBytes = 0
	facts.ReleaseBindingExact = false
	facts.ReceiptSetReady = false
	if got := EvaluateWhiteListPublication(facts).Verdict; got != WhiteListPublicationNoEntitlement {
		t.Fatalf("precedence verdict=%q", got)
	}

	facts = validWhiteListPublicationFacts()
	facts.PrimaryExpiresAtUnix = facts.NowUnix + 1
	facts.ReceiptsFreshUntilUnix = facts.NowUnix + 2
	facts.ObservedThroughUnix = facts.NowUnix - 1
	decision := EvaluateWhiteListPublication(facts)
	if decision.Verdict != WhiteListPublicationPublishable || decision.FreshUntilUnix != facts.NowUnix+1 {
		t.Fatalf("minimum freshness decision=%#v", decision)
	}
}

func TestEvaluateWhiteListPublicationUsesSeparateBoundedAdmissionDeadline(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(*WhiteListPublicationFacts)
		verdict WhiteListPublicationVerdict
		seconds int64
	}{
		{"awaiting first sample", func(*WhiteListPublicationFacts) {}, WhiteListPublicationPublishable, 3},
		{"shorter receipt", func(f *WhiteListPublicationFacts) { f.ReceiptsFreshUntilUnix = f.NowUnix + 2 }, WhiteListPublicationPublishable, 2},
		{"shorter primary access", func(f *WhiteListPublicationFacts) { f.PrimaryExpiresAtUnix = f.NowUnix + 1 }, WhiteListPublicationPublishable, 1},
		{"missing admission", func(f *WhiteListPublicationFacts) { f.AdmissionFreshUntilUnix = 0 }, WhiteListPublicationProjectionStale, 0},
		{"deadline boundary", func(f *WhiteListPublicationFacts) { f.AdmissionFreshUntilUnix = f.NowUnix }, WhiteListPublicationProjectionStale, 0},
		{"unbounded admission", func(f *WhiteListPublicationFacts) { f.AdmissionFreshUntilUnix = f.NowUnix + 6 }, WhiteListPublicationProjectionStale, 0},
		{"disabled", func(f *WhiteListPublicationFacts) { f.ActivationSource = WhiteListActivationDisabled }, WhiteListPublicationNoEntitlement, 0},
		{"pending balance", func(f *WhiteListPublicationFacts) { f.ProjectionPending = true }, WhiteListPublicationProjectionPending, 0},
		{"no balance", func(f *WhiteListPublicationFacts) { f.AvailableBytes = 0 }, WhiteListPublicationNoBalance, 0},
		{"release mismatch", func(f *WhiteListPublicationFacts) { f.ReleaseBindingExact = false }, WhiteListPublicationReleaseMismatch, 0},
		{"missing applied receipt", func(f *WhiteListPublicationFacts) { f.ReceiptSetReady = false }, WhiteListPublicationSidecarUnavailable, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := validWhiteListPublicationFacts()
			facts.ObservedThroughUnix = 0
			facts.AdmissionFreshUntilUnix = facts.NowUnix + 3
			test.mutate(&facts)
			decision := EvaluateWhiteListPublication(facts)
			wantUntil := int64(0)
			if test.seconds != 0 {
				wantUntil = facts.NowUnix + test.seconds
			}
			if decision.Verdict != test.verdict || decision.FreshUntilUnix != wantUntil || facts.ObservedThroughUnix != 0 {
				t.Fatalf("admission decision=%#v facts=%#v", decision, facts)
			}
		})
	}
}

func TestWhiteListPublicationRechecksClockAfterReadsWithoutExtendingDeadlines(t *testing.T) {
	facts := validWhiteListPublicationFacts()
	requestNow := facts.NowUnix
	facts.ObservedThroughUnix = 0 // Byte-budgeted admission has no synthetic usage watermark.
	facts.AdmissionFreshUntilUnix = requestNow + 7 // Five seconds from a read completed at requestNow+2.
	facts.ReceiptsFreshUntilUnix = requestNow + 120
	original := facts
	if got := EvaluateWhiteListPublication(facts).Verdict; got != WhiteListPublicationProjectionStale {
		t.Fatalf("fixture must reproduce the old request-clock rejection: %q", got)
	}
	service := &Service{clock: fixedClock{value: time.Unix(requestNow+2, 0)}}
	for _, offset := range []int64{2, 3} {
		service.clock = fixedClock{value: time.Unix(requestNow+offset, 0)}
		got := service.evaluateWhiteListPublicationAfterReads(facts, false)
		if got.Verdict != WhiteListPublicationPublishable || got.FreshUntilUnix != original.AdmissionFreshUntilUnix {
			t.Fatalf("clock advanced by %ds: deadline changed or fresh admission rejected: %#v", offset, got)
		}
	}
	service.clock = fixedClock{value: time.Unix(original.AdmissionFreshUntilUnix, 0)}
	if got := service.evaluateWhiteListPublicationAfterReads(facts, false); got != (WhiteListPublicationDecision{Verdict: WhiteListPublicationProjectionStale}) {
		t.Fatalf("expired admission did not close with zero metadata: %#v", got)
	}
	stable := service.evaluateWhiteListPublicationAfterReads(facts, true)
	if stable.Verdict != WhiteListPublicationPublishable || stable.FreshUntilUnix != original.PrimaryExpiresAtUnix {
		t.Fatalf("stable subscription expiry changed: %#v", stable)
	}
	if facts != original {
		t.Fatal("clock re-evaluation rewrote the collected facts or absolute deadlines")
	}
}

func publicationBatchRoutes() []WhiteListPublicationRoute {
	routes := make([]WhiteListPublicationRoute, 4)
	for index, country := range []string{"NL", "DE", "CZ", "ES"} {
		routes[index] = WhiteListPublicationRoute{ExitID: fmt.Sprintf("exit-s%d", index+1), CountryCode: country, CountryLabel: "Fixture " + country}
	}
	return routes
}

func TestWhiteListPublicationBatchAllocationsStayExactAndClosed(t *testing.T) {
	const entitlementID = "wl-ent-11111111111111111111111111111111"
	for _, test := range []struct {
		name string
		mutate func(*whiteListSidecarRuntimeState, []rqlite.Result)
		closed bool
		calls int
	}{
		{name: "all four exact", calls: 3},
		{name: "missing allocation", closed: true, calls: 3, mutate: func(_ *whiteListSidecarRuntimeState, rows []rqlite.Result) { rows[2].Rows = nil }},
		{name: "swapped allocation results", closed: true, calls: 3, mutate: func(_ *whiteListSidecarRuntimeState, rows []rqlite.Result) { rows[0], rows[1] = rows[1], rows[0] }},
		{name: "different boot", closed: true, calls: 3, mutate: func(_ *whiteListSidecarRuntimeState, rows []rqlite.Result) { rows[3].Rows[0]["xray_process_boot_id"] = "other-boot" }},
		{name: "different period", closed: true, calls: 3, mutate: func(_ *whiteListSidecarRuntimeState, rows []rqlite.Result) { rows[3].Rows[0]["billing_period_id"] = "other-period" }},
		{name: "last exit unhealthy", closed: true, calls: 0, mutate: func(state *whiteListSidecarRuntimeState, _ []rqlite.Result) { state.exits["exit-s4"] = WhiteListExit{Healthy: false} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{}
			service, _ := testService(t, db)
			now := service.clock.Now()
			routes := publicationBatchRoutes()
			period := whitelistbalance.Period{ID: "batch-period", StartsAtUnix: now.Unix()-10, EndsAtUnix: now.Unix()+3600, AccessOrderID: "batch-order"}
			projection := whitelistbalance.BalanceProjection{EntitlementID: entitlementID, CurrentPeriodID: period.ID, PurchasedRemainingBytes: 1_000_000_000, Version: 1, FreshThroughUnix: now.Unix()}
			state := whiteListSidecarRuntimeState{
				origins: []WhiteListOrigin{{OriginID: "origin-s4"}},
				publications: map[string]whiteListRuntimePublication{entitlementID: {Enabled: true, Source: WhiteListActivationAdminEnable, PrimaryStatus: "active", PrimaryExpiresAtUnix: now.Unix()+3600}},
				credentials: map[string]map[string]struct{}{entitlementID: {}}, exits: map[string]WhiteListExit{},
			}
			rows := make([]rqlite.Result, len(routes))
			for index, route := range routes {
				state.credentials[entitlementID][route.ExitID] = struct{}{}
				state.exits[route.ExitID] = WhiteListExit{ExitID: route.ExitID, Healthy: true}
				rows[index] = rqlite.Result{Rows: []map[string]any{{"entitlement_id": entitlementID, "exit_id": route.ExitID,
					"origin_id": "origin-s4", "xray_process_boot_id": "boot-s4", "billing_period_id": period.ID, "outstanding_bytes": int64(64_000_000)}}}
			}
			if test.mutate != nil { test.mutate(&state, rows) }
			db.linear = []scriptedResult{
				rowsScript(map[string]any{"period_id": period.ID, "ends_at_unix": period.EndsAtUnix}),
				rowsScript(whiteListBalanceStateRow(entitlementID, "active", now.Unix()+3600, period, projection, 0)),
				resultsScript(rows...),
			}
			receipts := map[string]WhiteListSidecarReceipt{"origin-s4": {XrayProcessBootID: "boot-s4", ExpiresAt: now.Add(120*time.Second)}}
			got := service.whiteListPublicationByteBudgetFreshUntil(context.Background(), entitlementID, routes, state, receipts, now)
			if (test.closed && got != 0) || (!test.closed && got != now.Unix()+5) {
				t.Fatalf("unexpected absolute deadline: %d", got)
			}
			if len(db.linearCalls) != test.calls || len(db.requestCalls) != 0 {
				t.Fatalf("read count=%d writes=%d", len(db.linearCalls), len(db.requestCalls))
			}
			if test.calls == 3 {
				if len(db.linearCalls[0].statements) != 1 || len(db.linearCalls[1].statements) != 1 || len(db.linearCalls[2].statements) != 4 {
					t.Fatal("period/balance was repeated or allocation batch was split")
				}
				for index, statement := range db.linearCalls[2].statements {
					if !reflect.DeepEqual(statement.Args, []any{entitlementID, routes[index].ExitID, "origin-s4", "boot-s4"}) {
						t.Fatal("allocation batch order or scope changed")
					}
				}
			}
		})
	}
}

func TestWhiteListPublicationBatchMaterialsStayExactAndClosed(t *testing.T) {
	const entitlementID = "wl-ent-11111111111111111111111111111111"
	for _, mode := range []string{"valid", "missing material", "swapped material", "wrong scope", "expired after read", "stable export"} {
		t.Run(mode, func(t *testing.T) {
			db := &recordingRQLite{}
			service, box := testService(t, db)
			now := service.clock.Now()
			routes := publicationBatchRoutes()
			rows := make([]rqlite.Result, len(routes))
			encryption := "mlkem768x25519plus.native.0rtt." + strings.Repeat("Wlpa", 394) + "Wlo"
			proof := sha256.Sum256([]byte("maestrovpn:vlessenc-client:v1\x00CLIENT\x00" + encryption))
			for index, route := range routes {
				material := WhiteListClientMaterial{PublicHost: "cdn.example.invalid", SecretPath: "/fixture/"+route.ExitID,
					ClientID: fmt.Sprintf("%08d-1111-4111-8111-%012d", index+1, index+1), ClientEncryption: encryption,
					ClientEncryptionRole: "CLIENT", ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:"+hex.EncodeToString(proof[:])}
				plaintext, err := json.Marshal(material)
				if err != nil { t.Fatal(err) }
				credential, err := NewWhiteListRouteCredential(box, entitlementID, route.ExitID, plaintext)
				if err != nil { t.Fatal(err) }
				envelope, err := json.Marshal(credential.Payload)
				if err != nil { t.Fatal(err) }
				rows[index] = rqlite.Result{Rows: []map[string]any{{"entitlement_id": entitlementID, "exit_id": route.ExitID,
					"managed_email": credential.ManagedEmail, "credential_envelope": envelope}}}
			}
			deadline := now.Unix()+5
			switch mode {
			case "missing material": rows[2].Rows = nil
			case "swapped material": rows[0], rows[1] = rows[1], rows[0]
			case "wrong scope": rows[3].Rows[0]["entitlement_id"] = "wl-ent-22222222222222222222222222222222"
			case "expired after read": service.clock = fixedClock{value: now.Add(6*time.Second)}
			case "stable export": deadline = 0; service.clock = fixedClock{value: now.Add(6*time.Second)}
			}
			db.linear = []scriptedResult{resultsScript(rows...)}
			err := service.fillWhiteListPublicationMaterials(context.Background(), entitlementID, routes, deadline)
			wantClosed := mode != "valid" && mode != "stable export"
			if (err != nil) != wantClosed { t.Fatalf("closed=%v, error=%v", wantClosed, err) }
			if len(db.linearCalls) != 1 || len(db.linearCalls[0].statements) != 4 || len(db.requestCalls) != 0 {
				t.Fatal("material reads did not remain one read-only batch")
			}
			for index, route := range routes {
				if !reflect.DeepEqual(db.linearCalls[0].statements[index].Args, []any{entitlementID, route.ExitID}) {
					t.Fatal("material batch order or scope changed")
				}
				if wantClosed && route.Material != (WhiteListClientMaterial{}) {
					t.Fatal("closed material batch leaked a partial route")
				}
				if !wantClosed && route.Material.ClientID != fmt.Sprintf("%08d-1111-4111-8111-%012d", index+1, index+1) {
					t.Fatal("successful material was attached to a different route")
				}
			}
		})
	}
}
