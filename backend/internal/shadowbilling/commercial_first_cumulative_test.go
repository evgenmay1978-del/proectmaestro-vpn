package shadowbilling

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestCommercialFirstCumulativePreservesGenericBaselineAndRejectsRearming(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, policy, source := newCommercialMeteringFixture(t, db, "first-pure")
	event := commercialMeteringEvent(policy, source, "first-pure-event", 1, 1, 19, 23, 2_000_010).OrderedUsageEvent
	_, baseline, err := ApplyOrdered(NewState(), event, policy)
	if err != nil || baseline.Diagnostic != DiagnosticEpochStarted || baseline.Interval != nil {
		t.Fatalf("generic baseline changed: %#v, %v", baseline, err)
	}
	next, first, err := applyFirstCumulative(NewState(), event, policy)
	if err != nil || first.Diagnostic != "" || first.Interval == nil ||
		first.Interval.UplinkBytes != 19 || first.Interval.DownlinkBytes != 23 {
		t.Fatalf("full first cumulative: %#v, %v", first, err)
	}
	checkpoint := next.counters[meterKey{event.InstanceID, event.MeterEpoch, event.XrayIdentity}]
	if checkpoint.up != 19 || checkpoint.down != 23 || checkpoint.generation != 1 || checkpoint.sequence != 1 {
		t.Fatalf("actual first checkpoint = %#v", checkpoint)
	}
	event.EventID = "cannot-rearm-first"
	if _, _, err := applyFirstCumulative(next, event, policy); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("existing counter rearmed: %v", err)
	}
}

func TestCommercialFirstCumulativeUsesAdmissionAndRealDebitWithUnknownCommitRecovery(t *testing.T) {
	ctx := context.Background()
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatal(err)
	}
	store, policy, physicalSource := newCommercialMeteringFixture(t, db, "first-real")
	policy.IncludedBytes, policy.SoftLimitBytes, policy.HardLimitBytes, policy.GraceBytes = 0, 0, 0, 0
	policy.Prices = PriceOptions{Global: &Price{Mode: PriceFree}}
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
	entitlementID := policy.EntitlementID()
	origin := controlplane.WhiteListOrigin{OriginID: physicalSource.OriginID, NodeID: "first-node",
		ReleaseID: "release-a", ProfileID: "profile-a", PresetID: "preset-a", ConfigDigest: strings.Repeat("a", 64), Active: true}
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES(?,?,0,1,1)`, Args: []any{origin.NodeID, origin.NodeID}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_exits(exit_id,country_code,country_label,healthy,created_at_unix) VALUES(?,'NL','Netherlands',1,1)`, Args: []any{physicalSource.ExitID}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_origins(origin_id,node_id,release_id,profile_id,preset_id,config_digest,active,created_at_unix) VALUES(?,?,?,?,?,?,1,1)`, Args: []any{origin.OriginID, origin.NodeID, origin.ReleaseID, origin.ProfileID, origin.PresetID, origin.ConfigDigest}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_balance_projections(entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix) VALUES(?,?,0,0,0,0,1,0,0,?)`, Args: []any{entitlementID, policy.BillingPeriodID(), clock.now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_publication_controls(control_id,entitlement_id,version,enabled,source,source_topup_order_id,operation_id,request_hash,created_at_unix) VALUES(?,?,2,1,'ADMIN_ENABLE',NULL,?,?,?)`, Args: []any{"first-enable", entitlementID, "first-enable-operation", strings.Repeat("b", 64), clock.now.Unix()}},
	)
	digest := sha256.Sum256([]byte("first-real"))
	if _, err := service.CreditWhiteListPurchasedBytes(ctx, clock.now.Unix(), controlplane.CreditWhiteListPurchasedBytesCommand{
		EntitlementID: entitlementID, PeriodID: policy.BillingPeriodID(),
		SourceOrderID: "order-" + hex.EncodeToString(digest[:])[:16], Bytes: 20_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	material, err := json.Marshal(controlplane.WhiteListClientMaterial{
		PublicHost: "cdn.example.invalid", SecretPath: "/static/main/video/segment.ts/opaque",
		ClientID: "11111111-1111-4111-8111-111111111111", ClientEncryption: "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole: "CLIENT", ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := controlplane.NewWhiteListRouteCredential(secrets, entitlementID, physicalSource.ExitID, material)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StoreWhiteListRouteCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	sender := &commercialFirstReceiptSender{now: clock.now, bootID: physicalSource.XrayProcessBootID}
	resolve := func(nodeID string) (controlplane.ExternalActionSender, bool) { return sender, nodeID == origin.NodeID }
	if err := service.EnsureWhiteListMeteringBootstrap(ctx, "first-worker", resolve); err != nil {
		t.Fatal(err)
	}
	if sender.receipt.DesiredGeneration != 1 {
		t.Fatalf("bootstrap receipt = %#v", sender.receipt)
	}
	if err := service.RecordWhiteListOriginObservation(ctx, controlplane.WhiteListOriginObservation{
		Receipt: sender.receipt, SampledAt: clock.now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.AuthorizeWhiteListMeteringAdmission(ctx, entitlementID, physicalSource.ExitID, controlplane.WhiteListAdmissionReserve{
		MeasuredP999BytesPerSecond: 2_000_000, MeasuredAtUnix: clock.now.Unix(), ValidUntilUnix: clock.now.Unix() + 20,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileWhiteListSidecarIntents(ctx, "first-worker", resolve); err != nil {
		t.Fatal(err)
	}
	if sender.receipt.DesiredGeneration != 2 {
		t.Fatalf("managed receipt = %#v", sender.receipt)
	}
	if err := service.RecordWhiteListOriginObservation(ctx, controlplane.WhiteListOriginObservation{
		Receipt: sender.receipt, SampledAt: clock.now, UnavailableUsers: []string{physicalSource.RouteXrayIdentity},
	}); err != nil {
		t.Fatal(err)
	}
	cursor, err := store.EnsureCommercialProducerCursor(ctx, physicalSource, clock.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	event := commercialMeteringEvent(policy, cursor.Source, "first-real-event", 1, 1, 19, 23, clock.now.Unix())
	event.MeterEpoch = cursor.MeterEpoch
	if _, err := store.ApplyCommercialFirstCumulative(ctx, event, policy, service); err == nil {
		t.Fatal("unavailable counters accepted as a first sample")
	}
	if commercialMeteringCount(t, db, "whitelist_metering_events", entitlementID) != 0 || meteringCheckpointCount(t, db, policy) != 0 {
		t.Fatal("unavailable counters wrote a metering event/checkpoint")
	}
	before, err := service.WhiteListBalanceSnapshot(ctx, clock.now.Unix(), entitlementID)
	if err != nil || before.Projection.FreshThroughUnix != 0 || before.AvailableBytes != 20_000_000 {
		t.Fatalf("health changed balance freshness: %#v, %v", before, err)
	}
	clock.now = clock.now.Add(time.Second)
	event.SampledAtUnix = clock.now.Unix()
	if err := service.RecordWhiteListOriginObservation(ctx, controlplane.WhiteListOriginObservation{
		Receipt: sender.receipt, SampledAt: clock.now, AvailableUsers: []string{physicalSource.RouteXrayIdentity},
	}); err != nil {
		t.Fatal(err)
	}
	// The real transaction commits, but its response and first debit callback fail.
	// Recovery must use the existing event/outbox, not fabricate a second interval.
	unknown := &commercialFirstUnknownCommit{RQLite: db, fail: true}
	store, err = NewDurableStore(unknown)
	if err != nil {
		t.Fatal(err)
	}
	debitUnavailable := errors.New("first debit unavailable")
	if _, err := store.ApplyCommercialFirstCumulative(ctx, event, policy, &commercialFailOnceDebiter{delegate: service, err: debitUnavailable}); !errors.Is(err, debitUnavailable) {
		t.Fatalf("pending first debit = %v", err)
	}
	if unknown.fail || commercialMeteringCount(t, db, "whitelist_commercial_debit_outbox", entitlementID) != 1 {
		t.Fatal("first transaction did not commit its real debit outbox")
	}
	// Current poll health may move on while an already committed debit is pending.
	clock.now = clock.now.Add(time.Second)
	if err := service.RecordWhiteListOriginObservation(ctx, controlplane.WhiteListOriginObservation{
		Receipt: sender.receipt, SampledAt: clock.now, UnavailableUsers: []string{physicalSource.RouteXrayIdentity},
	}); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewDurableStore(db)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := restarted.ApplyCommercialFirstCumulative(ctx, event, policy, service)
	if err != nil || replay.Decision.Diagnostic != "" || replay.Decision.Interval == nil ||
		replay.Decision.Interval.UplinkBytes != 19 || replay.Decision.Interval.DownlinkBytes != 23 {
		t.Fatalf("recover first cumulative: %#v, %v", replay, err)
	}
	if _, err := restarted.ApplyCommercialFirstCumulative(ctx, event, policy, service); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	if _, err := restarted.ApplyCommercialOrdered(ctx, event, policy, service); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("generic baseline aliased full-cumulative event: %v", err)
	}
	after, err := service.WhiteListBalanceSnapshot(ctx, clock.now.Unix(), entitlementID)
	if err != nil || after.AvailableBytes != 20_000_000-42 || after.Projection.LifetimeConsumedBytes != 42 ||
		after.Projection.FreshThroughUnix != event.SampledAtUnix || after.Projection.Version != before.Projection.Version+1 {
		t.Fatalf("actual applied first debit = %#v, %v", after, err)
	}
	for _, table := range []string{"whitelist_metering_events", "whitelist_commercial_metering_sources", "whitelist_metering_intervals", "whitelist_commercial_debit_outbox"} {
		if commercialMeteringCount(t, db, table, entitlementID) != 1 {
			t.Fatalf("duplicate or absent first accounting row in %s", table)
		}
	}
	plan, err := service.WhiteListMeteringPlan(ctx)
	if err != nil || len(plan.Origins) != 1 || len(plan.Origins[0].PendingFirstCumulativeUsers) != 0 {
		t.Fatalf("real receipt did not satisfy first-accounting proof: %#v, %v", plan, err)
	}
	resumed, err := restarted.EnsureCommercialProducerCursor(ctx, physicalSource, clock.now.Unix())
	if err != nil || resumed.Source != cursor.Source || resumed.MeterEpoch != cursor.MeterEpoch || resumed.NextSampleSequence != 2 {
		t.Fatalf("durable first cursor = %#v, %v", resumed, err)
	}
}

type commercialFirstClock struct{ now time.Time }

func (clock *commercialFirstClock) Now() time.Time { return clock.now }

type commercialFirstReceiptSender struct {
	now     time.Time
	bootID  string
	receipt controlplane.WhiteListSidecarReceipt
}

func (sender *commercialFirstReceiptSender) Post(_ context.Context, request []byte) ([]byte, error) {
	var payload struct {
		NodeID               string `json:"node_id"`
		OriginID             string `json:"origin_id"`
		ReleaseID            string `json:"release_id"`
		ConfigDigest         string `json:"config_digest"`
		Generation           int64  `json:"generation"`
		ManagedUserSetDigest string `json:"managed_user_set_digest"`
	}
	if err := json.Unmarshal(request, &payload); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(request)
	sender.receipt = controlplane.WhiteListSidecarReceipt{
		ActionKey: fmt.Sprintf("%s:%d:%s", payload.NodeID, payload.Generation, hex.EncodeToString(digest[:])),
		OriginID:  payload.OriginID, ReleaseID: payload.ReleaseID, XrayProcessBootID: sender.bootID,
		ConfigDigest: payload.ConfigDigest, DesiredGeneration: payload.Generation, ManagedUserSetDigest: payload.ManagedUserSetDigest,
		AppliedAt: sender.now, ExpiresAt: sender.now.Add(30 * time.Second),
	}
	return json.Marshal(sender.receipt)
}

func (sender *commercialFirstReceiptSender) LookupReceipt(context.Context, string) ([]byte, error) {
	return json.Marshal(sender.receipt)
}

type commercialFirstUnknownCommit struct {
	rqlite.RQLite
	fail bool
}

func (db *commercialFirstUnknownCommit) Request(ctx context.Context, consistency rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	results, err := db.RQLite.Request(ctx, consistency, transaction, statements...)
	if err == nil && db.fail && transaction {
		for _, statement := range statements {
			if strings.Contains(statement.SQL, "INSERT INTO whitelist_metering_events") {
				db.fail = false
				return nil, errors.New("metering response lost after commit")
			}
		}
	}
	return results, err
}
