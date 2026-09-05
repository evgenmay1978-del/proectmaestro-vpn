package shadowbilling

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
)

type commercialFinalFixture struct {
	db             *meteringSQLite
	store          *DurableStore
	service        *controlplane.Service
	policy         Policy
	physicalSource CommercialMeterSource
	authorization  controlplane.WhiteListFinalReceiptAuthorization
	final          sidecaragentclient.ManagedFinalReceipt
	nodeID         string
	clock          *commercialFirstClock
}

func newCommercialFinalFixture(t *testing.T, preprovisionUnused ...bool) *commercialFinalFixture {
	t.Helper()
	ctx := context.Background()
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatal(err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "final-real")
	policy.IncludedBytes, policy.SoftLimitBytes, policy.HardLimitBytes, policy.GraceBytes = 0, 0, 0, 0
	policy.Prices = PriceOptions{Global: &Price{Mode: PriceFree}}
	source.ExitID = "exit-s1"
	source.RouteXrayIdentity = "wl:" + policy.EntitlementID() + ":" + source.ExitID
	source.XrayProcessBootID = strings.Repeat("b", 64)
	source.CounterSourceID = "xray-api:" + source.OriginID + ":" + source.ExitID
	clock := &commercialFirstClock{now: time.Unix(2_000_010, 0).UTC()}
	secrets, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	controlStore, err := controlplane.NewStore(db, secrets, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(controlStore, &commercialControlPlaneIDs{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := "final-node"
	entitlementID := policy.EntitlementID()
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES(?,?,0,1,1)`, Args: []any{nodeID, nodeID}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_exits(exit_id,country_code,country_label,healthy,created_at_unix) VALUES(?,'NL','Netherlands',1,1)`, Args: []any{source.ExitID}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_origins(origin_id,node_id,release_id,profile_id,preset_id,config_digest,active,created_at_unix) VALUES(?,?,'release-a','profile-a','preset-a',?,1,1)`, Args: []any{source.OriginID, nodeID, strings.Repeat("a", 64)}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_balance_projections(entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix) VALUES(?,?,0,0,0,0,1,0,0,?)`, Args: []any{entitlementID, policy.BillingPeriodID(), clock.now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_publication_controls(control_id,entitlement_id,version,enabled,source,source_topup_order_id,operation_id,request_hash,created_at_unix) VALUES(?,?,2,1,'ADMIN_ENABLE',NULL,?,?,?)`, Args: []any{"final-enable", entitlementID, "final-enable-operation", strings.Repeat("c", 64), clock.now.Unix()}},
	)
	accountDigest := sha256.Sum256([]byte("final-real"))
	if _, err := service.CreditWhiteListPurchasedBytes(ctx, clock.now.Unix(), controlplane.CreditWhiteListPurchasedBytesCommand{EntitlementID: entitlementID, PeriodID: policy.BillingPeriodID(), SourceOrderID: "order-" + hex.EncodeToString(accountDigest[:])[:16], Bytes: 20_000_000}); err != nil {
		t.Fatal(err)
	}
	material, err := json.Marshal(controlplane.WhiteListClientMaterial{PublicHost: "cdn.example.invalid", SecretPath: "/static/main/video/segment.ts/opaque", ClientID: "11111111-1111-4111-8111-111111111111", ClientEncryption: "mlkem768x25519plus.native.0rtt.test-client-material", ClientEncryptionRole: "CLIENT", ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a"})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := controlplane.NewWhiteListRouteCredential(secrets, entitlementID, source.ExitID, material)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StoreWhiteListRouteCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	sender := &commercialFirstReceiptSender{now: clock.now, bootID: source.XrayProcessBootID}
	resolve := func(id string) (controlplane.ExternalActionSender, bool) { return sender, id == nodeID }
	if err := service.EnsureWhiteListMeteringBootstrap(ctx, "final-worker", resolve); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordWhiteListOriginObservation(ctx, controlplane.WhiteListOriginObservation{Receipt: sender.receipt, SampledAt: clock.now}); err != nil {
		t.Fatal(err)
	}
	if err := service.AuthorizeWhiteListMeteringAdmission(ctx, entitlementID, source.ExitID, controlplane.WhiteListAdmissionReserve{MeasuredP999BytesPerSecond: 2_000_000, MeasuredAtUnix: clock.now.Unix(), ValidUntilUnix: clock.now.Unix() + 20}); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileWhiteListSidecarIntents(ctx, "final-worker", resolve); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordWhiteListOriginObservation(ctx, controlplane.WhiteListOriginObservation{Receipt: sender.receipt, SampledAt: clock.now, UnavailableUsers: []string{source.RouteXrayIdentity}}); err != nil {
		t.Fatal(err)
	}
	up, down := int64(19), int64(23)
	clock.now = clock.now.Add(time.Second)
	final := sidecaragentclient.ManagedFinalReceipt{ActionKey: sender.receipt.ActionKey, OriginID: source.OriginID, ReleaseID: sender.receipt.ReleaseID, DesiredGeneration: sender.receipt.DesiredGeneration, ManagedUserSetDigest: sender.receipt.ManagedUserSetDigest,
		Control: sidecaragentclient.ManagedControl{Schema: 2, Operation: "fence", Email: source.RouteXrayIdentity, BootID: source.XrayProcessBootID, ConfigDigest: sender.receipt.ConfigDigest, Generation: 3, ClockDomain: strings.Repeat("d", 64)},
		Receipt: sidecaragentclient.ManagedReceipt{Schema: 2, State: "fenced", Email: source.RouteXrayIdentity, BootID: source.XrayProcessBootID, ConfigDigest: sender.receipt.ConfigDigest, Generation: 3, ObservedAt: clock.now.Format(time.RFC3339Nano), Uplink: &up, Downlink: &down, ClockDomain: strings.Repeat("d", 64)}}
	if len(preprovisionUnused) > 0 && preprovisionUnused[0] {
		final.Receipt.State = "fenced_unused"
		final.Receipt.Uplink = nil
		final.Receipt.Downlink = nil
		// Model the unknown first Apply response: desired/action is durable,
		// while neither current health nor successful receipt reached backend.
		db.must(t, rqlite.Statement{SQL: `DELETE FROM whitelist_metering_origin_observations WHERE origin_id=?`, Args: []any{source.OriginID}}, rqlite.Statement{SQL: `DELETE FROM whitelist_sidecar_receipts WHERE action_key=?`, Args: []any{final.ActionKey}})
	}
	sealCommercialFinalFixture(t, &final)
	authorization, err := service.AuthorizeWhiteListFinalReceipt(ctx, nodeID, final)
	if err != nil {
		t.Fatal(err)
	}
	return &commercialFinalFixture{db: db, store: store, service: service, policy: policy, physicalSource: source, authorization: authorization, final: final, nodeID: nodeID, clock: clock}
}

func sealCommercialFinalFixture(t *testing.T, final *sidecaragentclient.ManagedFinalReceipt) {
	t.Helper()
	identity := struct {
		ActionKey            string                            `json:"action_key"`
		OriginID             string                            `json:"origin_id"`
		ReleaseID            string                            `json:"release_id"`
		DesiredGeneration    int64                             `json:"desired_generation"`
		ManagedUserSetDigest string                            `json:"managed_user_set_digest"`
		Control              sidecaragentclient.ManagedControl `json:"control"`
	}{final.ActionKey, final.OriginID, final.ReleaseID, final.DesiredGeneration, final.ManagedUserSetDigest, final.Control}
	body, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	id := sha256.Sum256(body)
	final.ReceiptID = hex.EncodeToString(id[:])
	body, err = json.Marshal(final.Proof())
	if err != nil {
		t.Fatal(err)
	}
	proof := sha256.Sum256(body)
	final.ProofSHA256 = hex.EncodeToString(proof[:])
	if err := sidecaragentclient.ValidateManagedFinalReceipt(*final); err != nil {
		t.Fatal(err)
	}
}

func TestCommercialFinalFirstCounterSettlesAfterRemovalAndReplaysExactProof(t *testing.T) {
	f := newCommercialFinalFixture(t)
	ctx := context.Background()
	f.db.must(t, rqlite.Statement{SQL: `UPDATE whitelist_sidecar_origins SET active=0 WHERE origin_id=?`, Args: []any{f.physicalSource.OriginID}})
	before, err := f.service.WhiteListBalanceSnapshot(ctx, f.clock.now.Unix(), f.policy.EntitlementID())
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.ApplyCommercialFinalReceipt(ctx, f.authorization, f.service)
	if err != nil || result.Decision.Diagnostic != "" || result.Decision.Interval == nil || result.Decision.Interval.BillableBytes != 42 {
		t.Fatalf("terminal first counters: %#v %v", result, err)
	}
	if _, err := f.store.ApplyCommercialFinalReceipt(ctx, f.authorization, f.service); err != nil {
		t.Fatal(err)
	}
	after, err := f.service.WhiteListBalanceSnapshot(ctx, f.clock.now.Unix(), f.policy.EntitlementID())
	if err != nil || after.AvailableBytes != before.AvailableBytes-42 || after.Projection.LifetimeConsumedBytes != 42 {
		t.Fatalf("final debit: %#v %v", after, err)
	}
	copy := f.authorization.Receipt()
	*copy.Receipt.Uplink = 999
	if *f.authorization.Receipt().Receipt.Uplink != 19 {
		t.Fatal("authorization counter alias")
	}
}

func TestCommercialFinalUnusedPersistsNoCounterAndRejectsChangedProof(t *testing.T) {
	f := newCommercialFinalFixture(t)
	ctx := context.Background()
	unused := f.final
	unused.Control.Generation++
	unused.Receipt.Generation++
	unused.Receipt.State = "fenced_unused"
	unused.Receipt.Uplink = nil
	unused.Receipt.Downlink = nil
	sealCommercialFinalFixture(t, &unused)
	auth, err := f.service.AuthorizeWhiteListFinalReceipt(ctx, f.nodeID, unused)
	if err != nil || !auth.Unused() {
		t.Fatalf("unused: %v", err)
	}
	if _, err := f.store.ApplyCommercialFinalReceipt(ctx, auth, f.service); err == nil {
		t.Fatal("absent counters entered metering")
	}
	changed := f.final
	up := int64(20)
	changed.Receipt.Uplink = &up
	sealCommercialFinalFixture(t, &changed)
	if _, err := f.service.AuthorizeWhiteListFinalReceipt(ctx, f.nodeID, changed); err == nil {
		t.Fatal("same receipt replaced its proof")
	}
	if commercialMeteringCount(t, f.db, "whitelist_metering_events", f.policy.EntitlementID()) != 0 {
		t.Fatal("proof created counter event")
	}
}

func TestCommercialFinalUnusedBeforeBackendApplyReceiptDoesNotDeadlockBootstrap(t *testing.T) {
	f := newCommercialFinalFixture(t, true)
	if !f.authorization.Verified() || !f.authorization.Unused() || f.authorization.Receipt().Receipt.Uplink != nil || f.authorization.Receipt().Receipt.Downlink != nil {
		t.Fatal("preprovision proof manufactured counters")
	}
	rows, err := f.db.QueryLinearizable(context.Background(), rqlite.Statement{SQL: `SELECT action_key FROM whitelist_sidecar_receipts WHERE action_key=?`, Args: []any{f.final.ActionKey}})
	if err != nil || len(rows) != 1 || len(rows[0].Rows) != 0 {
		t.Fatal("test requires missing backend successful receipt")
	}
	if commercialMeteringCount(t, f.db, "whitelist_metering_events", f.policy.EntitlementID()) != 0 {
		t.Fatal("unused proof changed accounting")
	}
	used := f.final
	up, down := int64(0), int64(0)
	used.Control.Generation++
	used.Receipt.Generation++
	used.Receipt.State = "fenced"
	used.Receipt.Uplink = &up
	used.Receipt.Downlink = &down
	sealCommercialFinalFixture(t, &used)
	if _, err := f.service.AuthorizeWhiteListFinalReceipt(context.Background(), f.nodeID, used); err == nil {
		t.Fatal("real counters bypassed successful receipt binding")
	}
}
