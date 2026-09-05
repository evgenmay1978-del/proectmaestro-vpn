package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

func TestWhiteListPublicationDeliveryReadsCurrentDurableStateBySubscriptionToken(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	now := service.clock.Now()
	customer := seedIntegrityCustomer(t, service)
	access, err := service.customerAccess(ctx, customer.ID)
	if err != nil {
		t.Fatalf("customerAccess: %v", err)
	}
	entitlement, err := service.EnsureWhiteListEntitlement(ctx, customer.ID)
	if err != nil {
		t.Fatalf("EnsureWhiteListEntitlement: %v", err)
	}
	entitlementID := entitlement.EntitlementID()
	origin := WhiteListOrigin{
		OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1",
		ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("a"),
		Active: true, StaticUsers: []string{"static-s2@example.invalid"},
	}
	exit := WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true}
	seedWhiteListSidecarInventory(t, db, []WhiteListOrigin{origin}, exit)
	materialBytes, err := json.Marshal(WhiteListClientMaterial{
		PublicHost: "cdn.example.invalid", SecretPath: "/static/main/video/segment.ts/opaque",
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := NewWhiteListRouteCredential(service.store.secrets, entitlementID, exit.ExitID, materialBytes)
	if err != nil {
		t.Fatalf("NewWhiteListRouteCredential: %v", err)
	}
	if err := service.StoreWhiteListRouteCredential(ctx, credential); err != nil {
		t.Fatalf("StoreWhiteListRouteCredential: %v", err)
	}
	const periodID = "delivery-paid-period"
	const accessOrderID = "delivery-paid-access-order"
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id)
VALUES(?,'AA1122334455','whitelist-delivery',?,?,'tariff_1m_v1',40000,'RUB',30,?,?,'confirmed','applied','confirmed',?,?,1,?)`,
			Args: []any{accessOrderID, testDigest("c"), customer.ID, now.Unix() - 100,
				now.Unix() - 100 + 86400, now.Unix() - 50, now.Unix() + 30*86400, "delivery-paid-operation"}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix)
VALUES(?,?,0,?,?,0,?,?)`, Args: []any{
			periodID, entitlementID, now.Unix() - 100, now.Unix() + 86400, accessOrderID, now.Unix(),
		}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_balance_projections(
entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix
) VALUES(?,?,0,1000000000,0,0,1,0,0,?)`, Args: []any{entitlementID, periodID, now.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_publication_controls(
control_id,entitlement_id,version,enabled,source,source_topup_order_id,
operation_id,request_hash,created_at_unix
) VALUES(?,?,2,1,'ADMIN_ENABLE',NULL,?,?,?)`, Args: []any{
			"wlpub-admin:" + entitlementID, entitlementID, "op-enable:" + entitlementID,
			testDigest("b"), now.Unix(),
		}},
	)
	sender := &desiredReceiptSender{now: now, bootID: "boot-s2"}
	resolveSender := func(nodeID string) (ExternalActionSender, bool) {
		return sender, nodeID == origin.NodeID
	}
	if err := service.EnsureWhiteListMeteringBootstrap(ctx, "worker-s2", resolveSender); err != nil {
		t.Fatalf("EnsureWhiteListMeteringBootstrap: %v", err)
	}
	bootstrapReceipt, err := decodeWhiteListSidecarReceipt(sender.receipt)
	if err != nil || bootstrapReceipt.DesiredGeneration != 1 {
		t.Fatalf("empty bootstrap receipt = %#v, %v", bootstrapReceipt, err)
	}
	bootstrapObservation := WhiteListOriginObservation{
		Receipt: bootstrapReceipt, SampledAt: now, AvailableUsers: []string{}, UnavailableUsers: []string{},
	}
	wrongBootObservation := bootstrapObservation
	wrongBootObservation.Receipt.XrayProcessBootID = "wrong-boot"
	if err := service.RecordWhiteListOriginObservation(ctx, wrongBootObservation); err == nil {
		t.Fatal("collector health accepted a boot identity different from the durable receipt")
	}
	if err := service.RecordWhiteListOriginObservation(ctx, bootstrapObservation); err != nil {
		t.Fatalf("record empty collector health: %v", err)
	}
	if err := service.AuthorizeWhiteListMeteringAdmission(ctx, entitlementID, exit.ExitID, WhiteListAdmissionReserve{}); err == nil {
		t.Fatal("first-use admission accepted a missing reserve measurement")
	}
	missingReserveResults, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT COUNT(*) AS admissions
FROM whitelist_first_use_admissions WHERE entitlement_id=? AND exit_id=?`, Args: []any{entitlementID, exit.ExitID}})
	missingReserveRow, hasMissingReserveRow := firstRow(missingReserveResults)
	admissionCount, countOK := rowInt64(missingReserveRow, "admissions")
	if err != nil || !hasMissingReserveRow || !countOK || admissionCount != 0 {
		t.Fatalf("missing reserve created a durable first-use admission: %#v, %v", missingReserveResults, err)
	}
	reserve := WhiteListAdmissionReserve{
		MeasuredP999BytesPerSecond: 2_000_000, MeasuredAtUnix: now.Unix(), ValidUntilUnix: now.Unix() + 20,
	}
	if err := service.AuthorizeWhiteListMeteringAdmission(ctx, entitlementID, exit.ExitID, reserve); err != nil {
		t.Fatalf("authorize new first-use admission: %v", err)
	}
	if err := service.ReconcileWhiteListSidecarIntents(ctx, "worker-s2", resolveSender); err != nil {
		t.Fatalf("ReconcileWhiteListSidecarIntents: %v", err)
	}
	currentReceipt, err := decodeWhiteListSidecarReceipt(sender.receipt)
	if err != nil || currentReceipt.DesiredGeneration != 2 {
		t.Fatalf("managed-user receipt = %#v, %v", currentReceipt, err)
	}
	routeIdentity := "wl:" + entitlementID + ":" + exit.ExitID
	if err := service.RecordWhiteListOriginObservation(ctx, WhiteListOriginObservation{
		Receipt: currentReceipt, SampledAt: now, UnavailableUsers: []string{routeIdentity},
	}); err != nil {
		t.Fatalf("record unavailable first-use counters: %v", err)
	}
	service, err = NewService(service.store, service.ids, service.clock)
	if err != nil {
		t.Fatalf("restart service while awaiting first use: %v", err)
	}
	if err := service.AuthorizeWhiteListMeteringAdmission(ctx, entitlementID, exit.ExitID, reserve); err != nil {
		t.Fatalf("replay awaiting first-use admission: %v", err)
	}
	admissionStateRead := rqlite.Statement{SQL: `SELECT admission.billing_period_id,admission.first_observed_at_unix,projection.fresh_through_unix
FROM whitelist_first_use_admissions AS admission
JOIN whitelist_balance_projections AS projection ON projection.entitlement_id=admission.entitlement_id
WHERE admission.entitlement_id=? AND admission.exit_id=? AND admission.origin_id=? AND admission.xray_process_boot_id=?`,
		Args: []any{entitlementID, exit.ExitID, origin.OriginID, "boot-s2"}}
	admissionResults, err := db.QueryLinearizable(ctx, admissionStateRead)
	admissionRow, found := firstRow(admissionResults)
	firstObserved, observedOK := rowInt64(admissionRow, "first_observed_at_unix")
	freshThrough, freshOK := rowInt64(admissionRow, "fresh_through_unix")
	if err != nil || !found || !observedOK || !freshOK || firstObserved != 0 || freshThrough != 0 {
		t.Fatalf("awaiting admission replay fabricated usage freshness: %#v, %v", admissionResults, err)
	}
	awaiting, err := service.WhiteListPublicationDelivery(ctx, access.SubscriptionToken, now, resolveSender)
	if err != nil || awaiting.Decision.Verdict == WhiteListPublicationPublishable ||
		awaiting.Material != (WhiteListClientMaterial{}) {
		t.Fatalf("unavailable first-use counters published material: %#v, %v", awaiting, err)
	}

	// This is a synthetic immutable accounting proof, not a call through the
	// generic EPOCH_STARTED path, which still discards its first cumulative.
	now = now.Add(time.Second)
	service.clock = fixedClock{value: now}
	debit := seedWhiteListDeliveryFirstCumulative(t, db, customer.ID, entitlementID, periodID,
		origin.OriginID, exit.ExitID, currentReceipt.XrayProcessBootID, now.Unix())
	if err := service.DebitCommercialInterval(ctx, debit); err != nil {
		t.Fatalf("debit all first cumulative bytes: %v", err)
	}
	snapshot, err := service.WhiteListBalanceSnapshot(ctx, now.Unix(), entitlementID)
	if err != nil || snapshot.Projection.Version != 2 || snapshot.Projection.Pending ||
		snapshot.Projection.PurchasedRemainingBytes != 999_999_900 ||
		snapshot.Projection.LifetimeConsumedBytes != 100 || snapshot.Projection.UncoveredBytes != 0 ||
		snapshot.Projection.FreshThroughUnix != now.Unix() {
		t.Fatalf("first cumulative was not fully debited: %#v, %v", snapshot, err)
	}
	results, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT COUNT(*) AS applied_debits
FROM whitelist_commercial_debit_outbox AS outbox
JOIN idempotency_requests AS receipt ON receipt.scope='whitelist-balance' AND receipt.command_type='apply-usage'
 AND receipt.idempotency_key=outbox.receipt_key AND receipt.request_hash=outbox.request_hash
 AND receipt.resource_id=outbox.entitlement_id AND receipt.status='applied'
WHERE outbox.event_id=?`, Args: []any{debit.IntervalID}})
	row, found := firstRow(results)
	applied, valid := rowInt64(row, "applied_debits")
	if err != nil || !found || !valid || applied != 1 {
		t.Fatalf("first cumulative lacks exact applied outbox receipt: %#v, %v", results, err)
	}
	if err := service.RecordWhiteListOriginObservation(ctx, WhiteListOriginObservation{
		Receipt: currentReceipt, SampledAt: now, AvailableUsers: []string{routeIdentity},
	}); err != nil {
		t.Fatalf("record fully accounted available counters: %v", err)
	}
	reserve.MeasuredAtUnix, reserve.ValidUntilUnix = now.Unix(), now.Unix()+20
	if err := service.AuthorizeWhiteListMeteringAdmission(ctx, entitlementID, exit.ExitID, reserve); err != nil {
		t.Fatalf("refresh existing accounted admission reserve: %v", err)
	}

	got, err := service.WhiteListPublicationDelivery(ctx, access.SubscriptionToken, now, resolveSender)
	if err != nil {
		t.Fatalf("WhiteListPublicationDelivery: %v", err)
	}
	if got.Decision.Verdict != WhiteListPublicationPublishable || got.Decision.ProjectionVersion != 2 ||
		got.Decision.DesiredGeneration != 2 || got.Material.ClientID != "11111111-1111-4111-8111-111111111111" ||
		got.CountryCode != "NL" || got.CountryLabel != "Netherlands" || got.ReleaseID != origin.ReleaseID {
		t.Fatalf("publication delivery = %#v", got)
	}

	sender.receipt = nil
	restarted, err := service.WhiteListPublicationDelivery(ctx, access.SubscriptionToken, now, resolveSender)
	if err != nil || restarted.Decision.Verdict == WhiteListPublicationPublishable {
		t.Fatalf("receipt from a prior Xray boot remained publishable: %#v, %v", restarted, err)
	}

	closed, err := service.WhiteListPublicationDelivery(ctx, "unknown-token", now, resolveSender)
	if err != nil || closed.Decision.Verdict != WhiteListPublicationNoEntitlement {
		t.Fatalf("unknown token decision = %#v, %v", closed, err)
	}

	now = now.Add(time.Second)
	service.clock = fixedClock{value: now}
	if err := service.RecordWhiteListOriginObservation(ctx, WhiteListOriginObservation{
		Receipt: currentReceipt, SampledAt: now, UnavailableUsers: []string{routeIdentity},
	}); err != nil {
		t.Fatalf("record missing counters after first use: %v", err)
	}
	if _, ready := service.whiteListMeteringAdmissionReady(ctx, entitlementID, exit.ExitID, true); ready {
		t.Fatal("missing counters after first use reopened provisioning admission")
	}
	service, err = NewService(service.store, service.ids, service.clock)
	if err != nil {
		t.Fatalf("restart service after counters disappeared: %v", err)
	}
	reserve.MeasuredAtUnix, reserve.ValidUntilUnix = now.Unix(), now.Unix()+20
	if err := service.AuthorizeWhiteListMeteringAdmission(ctx, entitlementID, exit.ExitID, reserve); err != nil {
		t.Fatalf("refresh durable admission after counters disappeared: %v", err)
	}
	admissionResults, err = db.QueryLinearizable(ctx, admissionStateRead)
	admissionRow, found = firstRow(admissionResults)
	firstObserved, observedOK = rowInt64(admissionRow, "first_observed_at_unix")
	freshThrough, freshOK = rowInt64(admissionRow, "fresh_through_unix")
	if err != nil || !found || !observedOK || !freshOK ||
		firstObserved != debit.IntervalEndUnix || freshThrough != debit.IntervalEndUnix {
		t.Fatalf("restart reset the first-use latch or advanced missing-counter freshness: %#v, %v", admissionResults, err)
	}
	if _, ready := service.whiteListMeteringAdmissionReady(ctx, entitlementID, exit.ExitID, true); ready {
		t.Fatal("restart and reserve refresh reopened missing-after-seen admission")
	}

	// A legitimate next paid period cannot reopen the same admitted lifetime.
	const nextPeriodID = "delivery-next-paid-period"
	const nextAccessOrderID = "delivery-next-paid-access-order"
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id)
VALUES(?,'BB1122334455','whitelist-delivery',?,?,'tariff_1m_v1',40000,'RUB',30,?,?,'confirmed','applied','confirmed',?,?,1,?)`,
			Args: []any{nextAccessOrderID, testDigest("c"), customer.ID, now.Unix() - 100,
				now.Unix() - 100 + 86400, now.Unix() - 50, now.Unix() + 30*86400, "delivery-next-paid-operation"}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix)
SELECT ?,entitlement_id,1,ends_at_unix,ends_at_unix+86400,0,?,?
FROM whitelist_billing_periods WHERE period_id=?`,
			Args: []any{nextPeriodID, nextAccessOrderID, now.Unix(), periodID}},
	)
	_, err = db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `UPDATE whitelist_first_use_admissions
SET billing_period_id=?,first_observed_at_unix=0
WHERE entitlement_id=? AND exit_id=? AND origin_id=? AND xray_process_boot_id=?`,
		Args: []any{nextPeriodID, entitlementID, exit.ExitID, origin.OriginID, "boot-s2"}})
	var statementErr *rqlite.StatementError
	if !errors.As(err, &statementErr) ||
		(statementErr.Message != "white-list first-use admission binding is immutable" &&
			statementErr.Message != "white-list first-use observation cannot be reopened") {
		t.Fatalf("paid-period rollover did not reject admission rearming: %v", err)
	}
	admissionResults, err = db.QueryLinearizable(ctx, admissionStateRead)
	admissionRow, found = firstRow(admissionResults)
	boundPeriod, periodOK := rowString(admissionRow, "billing_period_id")
	firstObserved, observedOK = rowInt64(admissionRow, "first_observed_at_unix")
	freshThrough, freshOK = rowInt64(admissionRow, "fresh_through_unix")
	if err != nil || !found || !periodOK || !observedOK || !freshOK || boundPeriod != periodID ||
		firstObserved != debit.IntervalEndUnix || freshThrough != debit.IntervalEndUnix {
		t.Fatalf("period rollover changed the immutable admission or usage watermark: %#v, %v", admissionResults, err)
	}
}

// The fixture writes one first real counter sample plus its exact outbox
// request; only DebitCommercialInterval may create its applied debit receipt.
func seedWhiteListDeliveryFirstCumulative(
	t *testing.T, db *customerIntegritySQLite, accountID, entitlementID, periodID, originID, exitID, bootID string,
	sampledAtUnix int64,
) whitelistmetering.CommercialDebit {
	t.Helper()
	const eventID = "delivery-first-interval"
	const meterEpoch = "delivery-origin-s2-boot-s2"
	const counterSourceID = "delivery-counter-s2"
	baseIdentity := "wl:" + entitlementID
	routeIdentity := baseIdentity + ":" + exitID
	sourceSHA256, err := whitelistmetering.SourceSHA256(whitelistmetering.SourceDigestInput{
		AccountID: accountID, EntitlementID: entitlementID, TransportID: "yandex-cdn", BillingPeriodID: periodID,
		Basis: "UPLINK_PLUS_DOWNLINK", BaseXrayIdentity: baseIdentity, RouteXrayIdentity: routeIdentity,
		OriginID: originID, ExitID: exitID, CounterSourceID: counterSourceID, XrayProcessBootID: bootID,
		ResetSequence: 0, MeterEpoch: meterEpoch, CounterGeneration: 1, SampleSequence: 1,
		UplinkBytes: 40, DownlinkBytes: 60, SampledAtUnix: sampledAtUnix,
	})
	if err != nil {
		t.Fatalf("first cumulative source digest: %v", err)
	}
	debit := whitelistmetering.CommercialDebit{
		EntitlementID: entitlementID, BillingPeriodID: periodID, MeterEpoch: meterEpoch, IntervalID: eventID,
		Basis: "UPLINK_PLUS_DOWNLINK", IntervalEndUnix: sampledAtUnix, SourceSHA256: sourceSHA256,
	}
	receiptKey, err := whitelistmetering.CommercialDebitReceiptKey(meterEpoch, eventID)
	if err != nil {
		t.Fatal(err)
	}
	requestHash, err := whitelistmetering.CommercialDebitReceiptHash(debit)
	if err != nil {
		t.Fatal(err)
	}
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO whitelist_metering_periods(
entitlement_id,billing_period_id,account_id,transport_id,xray_identity,unit,basis,
included_bytes,soft_limit_bytes,hard_limit_bytes,grace_bytes,price_mode,price_source,
currency,minor_units_per_unit,policy_sha256,created_at_unix)
VALUES(?,?,?,'yandex-cdn',?,'GB_DECIMAL','UPLINK_PLUS_DOWNLINK','0','0','0','0','FREE','GLOBAL','','0',?,?)`,
			Args: []any{entitlementID, periodID, accountID, baseIdentity, testDigest("d"), sampledAtUnix}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_meter_epochs(
meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix)
VALUES(?,?,?,?,0,?)`, Args: []any{meterEpoch, originID, counterSourceID, bootID, sampledAtUnix}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_metering_events(
event_id,entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity,
counter_generation,sample_sequence,uplink_bytes,downlink_bytes,payload_sha256,
diagnostic,has_interval,result_json,created_at_unix)
VALUES(?,?,?,?,?,?,'1','1','40','60',?,'',1,'{}',?)`,
			Args: []any{eventID, entitlementID, periodID, originID, meterEpoch, baseIdentity, testDigest("e"), sampledAtUnix}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_metering_intervals(
event_id,uplink_delta_bytes,downlink_delta_bytes,billable_bytes,
amount_numerator,amount_denominator,currency,created_at_unix)
VALUES(?,'40','60','100','0','1','',?)`, Args: []any{eventID, sampledAtUnix}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_commercial_metering_sources(
event_id,entitlement_id,billing_period_id,origin_id,exit_id,meter_epoch,
route_xray_identity,counter_generation,sample_sequence,basis,sampled_at_unix,source_sha256)
VALUES(?,?,?,?,?,?,?,'1','1','UPLINK_PLUS_DOWNLINK',?,?)`,
			Args: []any{eventID, entitlementID, periodID, originID, exitID, meterEpoch, routeIdentity, sampledAtUnix, sourceSHA256}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_commercial_debit_outbox(
event_id,entitlement_id,billing_period_id,meter_epoch,basis,interval_end_unix,
source_sha256,receipt_key,request_hash,created_at_unix)
VALUES(?,?,?,?,'UPLINK_PLUS_DOWNLINK',?,?,?,?,?)`,
			Args: []any{eventID, entitlementID, periodID, meterEpoch, sampledAtUnix, sourceSHA256, receiptKey, requestHash, sampledAtUnix}},
	)
	return debit
}

func TestWhiteListClientMaterialRejectsNonClientCredential(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	customer := seedIntegrityCustomer(t, service)
	entitlement, err := service.EnsureWhiteListEntitlement(ctx, customer.ID)
	if err != nil {
		t.Fatal(err)
	}
	exit := WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true}
	seedWhiteListSidecarInventory(t, db, []WhiteListOrigin{{
		OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", ProfileID: "profile-1",
		PresetID: "preset-1", ConfigDigest: testDigest("a"), Active: true,
	}}, exit)
	badBytes, err := json.Marshal(WhiteListClientMaterial{
		PublicHost: "cdn.example.invalid", SecretPath: "/static/main/video/segment.ts/opaque",
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "none",
		ClientEncryptionRole:     "SERVER",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := NewWhiteListRouteCredential(service.store.secrets, entitlement.EntitlementID(), exit.ExitID, badBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StoreWhiteListRouteCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	if _, err := service.whiteListClientMaterial(ctx, entitlement.EntitlementID(), exit.ExitID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("non-client route credential accepted: %v", err)
	}
}
