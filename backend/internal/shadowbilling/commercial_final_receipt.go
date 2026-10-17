package shadowbilling

import (
	"context"
	"encoding/json"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type commercialFinalProof struct {
	authorization   controlplane.WhiteListFinalReceiptAuthorization
	event           CommercialOrderedUsageEvent
	firstCumulative bool
}

type commercialFinalAccepted struct {
	Schema          int                         `json:"schema"`
	ReceiptID       string                      `json:"receipt_id"`
	ProofSHA256     string                      `json:"proof_sha256"`
	Event           CommercialOrderedUsageEvent `json:"event"`
	FirstCumulative bool                        `json:"first_cumulative"`
}

// ApplyCommercialFinalReceipt accepts a real fenced cumulative counter and
// drains its durable debit before returning. The accepted event identity,
// sequence and arithmetic mode are immutable across uncertain outcomes. A
// never-used receipt has no counter and never enters this metering method.
func (store *DurableStore) ApplyCommercialFinalReceipt(ctx context.Context, authorization controlplane.WhiteListFinalReceiptAuthorization, debiter CommercialDebiter) (DurableResult, error) {
	if store == nil || store.db == nil || ctx == nil || debiter == nil || !authorization.Verified() || authorization.Unused() {
		return DurableResult{}, ErrInvalidInput
	}
	route := authorization.Route()
	final := authorization.Receipt()
	policy, err := NewPolicy(route.Entitlement, PolicySpec{BillingPeriodID: route.Policy.BillingPeriodID, Unit: UnitGBDecimal, Basis: BasisUplinkPlusDownlink, Prices: PriceOptions{Global: &Price{Mode: PriceFree}}})
	if err != nil {
		return DurableResult{}, err
	}
	rows, err := store.db.QueryLinearizable(ctx, commercialFinalAcceptedRead(final.ReceiptID))
	if err != nil || len(rows) != 1 || len(rows[0].Rows) > 1 {
		return DurableResult{}, ErrDurableStateInvalid
	}
	var accepted commercialFinalAccepted
	if len(rows[0].Rows) == 1 {
		body, err := rowString(rows[0].Rows[0], "response_json")
		if err != nil || json.Unmarshal([]byte(body), &accepted) != nil || accepted.Schema != 1 || accepted.ReceiptID != final.ReceiptID || accepted.ProofSHA256 != final.ProofSHA256 {
			return DurableResult{}, ErrDurableStateInvalid
		}
	} else {
		// No separate committed sequence reservation: this cursor may race an
		// ordinary writer, and the common acceptance transaction resolves it.
		observed, err := time.Parse(time.RFC3339Nano, final.Receipt.ObservedAt)
		if err != nil {
			return DurableResult{}, ErrInvalidInput
		}
		physical := CommercialMeterSource{OriginID: final.OriginID, ExitID: route.ExitID, CounterSourceID: "xray-api:" + final.OriginID + ":" + route.ExitID, XrayProcessBootID: final.Control.BootID, ResetSequence: final.Receipt.ResetSequence, RouteXrayIdentity: route.ManagedEmail}
		cursor, err := store.EnsureCommercialProducerCursor(ctx, physical, observed.Unix())
		if err != nil {
			return DurableResult{}, err
		}
		identity, ok := route.Entitlement.XrayIdentity()
		if !ok || final.Receipt.Uplink == nil || final.Receipt.Downlink == nil {
			return DurableResult{}, ErrInvalidInput
		}
		accepted = commercialFinalAccepted{Schema: 1, ReceiptID: final.ReceiptID, ProofSHA256: final.ProofSHA256, FirstCumulative: cursor.NextSampleSequence == 1, Event: CommercialOrderedUsageEvent{OrderedUsageEvent: OrderedUsageEvent{UsageEvent: UsageEvent{EventID: authorization.EventID(), InstanceID: final.OriginID, MeterEpoch: cursor.MeterEpoch, XrayIdentity: identity, UplinkBytes: uint64(*final.Receipt.Uplink), DownlinkBytes: uint64(*final.Receipt.Downlink)}, CounterGeneration: 1, SampleSequence: cursor.NextSampleSequence}, Source: cursor.Source, SampledAtUnix: observed.Unix()}}
	}
	proof := &commercialFinalProof{authorization: authorization, event: accepted.Event, firstCumulative: accepted.FirstCumulative}
	return store.applyCommercialFinalOrdered(ctx, accepted.Event, policy, debiter, accepted.FirstCumulative, proof)
}

func commercialFinalAcceptedRead(receiptID string) rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT request_hash,resource_id,response_json,status,operation_id FROM idempotency_requests WHERE scope='whitelist-final-metering' AND command_type='accept-final-source' AND idempotency_key=?`, Args: []any{receiptID}}
}

func commercialFinalAcceptanceBody(source CommercialSourceBinding, proof *commercialFinalProof) (string, string, error) {
	if proof == nil || !proof.authorization.Verified() || proof.authorization.Unused() {
		return "", "", ErrInvalidInput
	}
	final := proof.authorization.Receipt()
	route := proof.authorization.Route()
	observed, err := time.Parse(time.RFC3339Nano, final.Receipt.ObservedAt)
	if err != nil || final.Receipt.Uplink == nil || final.Receipt.Downlink == nil || source.EventID != proof.authorization.EventID() || source.AccountID != route.Entitlement.AccountID() || source.EntitlementID != route.Entitlement.EntitlementID() || source.TransportID != route.Entitlement.TransportProfileID() || source.BillingPeriodID != route.Policy.BillingPeriodID || source.OriginID != final.OriginID || source.ExitID != route.ExitID || source.RouteXrayIdentity != final.Control.Email || source.XrayProcessBootID != final.Control.BootID || source.ResetSequence != final.Receipt.ResetSequence || source.UplinkBytes != uint64(*final.Receipt.Uplink) || source.DownlinkBytes != uint64(*final.Receipt.Downlink) || source.SampledAtUnix != observed.Unix() || source.SampledAtUnix < route.Policy.PeriodStartsAtUnix || source.SampledAtUnix >= route.Policy.PeriodEndsAtUnix || proof.firstCumulative != (source.SampleSequence == 1) {
		return "", "", ErrInvalidInput
	}
	policy, err := NewPolicy(route.Entitlement, PolicySpec{BillingPeriodID: route.Policy.BillingPeriodID, Unit: UnitGBDecimal, Basis: BasisUplinkPlusDownlink, Prices: PriceOptions{Global: &Price{Mode: PriceFree}}})
	if err != nil {
		return "", "", err
	}
	exact, err := BindCommercialMeteringSource(proof.event, policy)
	if err != nil || exact != source {
		return "", "", ErrInvalidInput
	}
	body, err := json.Marshal(commercialFinalAccepted{Schema: 1, ReceiptID: final.ReceiptID, ProofSHA256: final.ProofSHA256, Event: proof.event, FirstCumulative: proof.firstCumulative})
	if err != nil {
		return "", "", ErrInvalidInput
	}
	finalBody, err := json.Marshal(final)
	if err != nil {
		return "", "", ErrInvalidInput
	}
	return string(body), string(finalBody), nil
}

func commercialFinalAcceptanceStatements(source CommercialSourceBinding, proof *commercialFinalProof) ([]rqlite.Statement, error) {
	body, finalBody, err := commercialFinalAcceptanceBody(source, proof)
	if err != nil {
		return nil, err
	}
	final := proof.authorization.Receipt()
	return []rqlite.Statement{
		{SQL: `SELECT CASE WHEN EXISTS(SELECT 1 FROM idempotency_requests WHERE scope='whitelist-final-proof' AND command_type='accept-agent-fence' AND idempotency_key=? AND request_hash=? AND resource_id=? AND status='applied' AND response_json=?) THEN 1 ELSE abs(-9223372036854775808) END AS final_authorization_guard`, Args: []any{final.ReceiptID, final.ProofSHA256, source.EntitlementID, finalBody}},
		{SQL: `INSERT OR IGNORE INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,response_json,created_at_unix,applied_at_unix) VALUES('whitelist-final-metering','accept-final-source',?,?,?,'accepted',?,'applied',?,?,?)`, Args: []any{final.ReceiptID, source.SourceSHA256, source.EntitlementID, source.EventID, body, source.SampledAtUnix, source.SampledAtUnix}},
		{SQL: `SELECT CASE WHEN EXISTS(SELECT 1 FROM idempotency_requests WHERE scope='whitelist-final-metering' AND command_type='accept-final-source' AND idempotency_key=? AND request_hash=? AND resource_id=? AND operation_id=? AND status='applied' AND response_json=?) THEN 1 ELSE abs(-9223372036854775808) END AS final_source_guard`, Args: []any{final.ReceiptID, source.SourceSHA256, source.EntitlementID, source.EventID, body}},
	}, nil
}

func verifyCommercialFinalAcceptance(ctx context.Context, db rqlite.RQLite, source CommercialSourceBinding, proof *commercialFinalProof) error {
	body, _, err := commercialFinalAcceptanceBody(source, proof)
	if err != nil {
		return err
	}
	results, err := db.QueryLinearizable(ctx, commercialFinalAcceptedRead(proof.authorization.Receipt().ReceiptID))
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		return ErrDurableStateInvalid
	}
	row := results[0].Rows[0]
	if row["request_hash"] != source.SourceSHA256 || row["resource_id"] != source.EntitlementID || row["operation_id"] != source.EventID || row["status"] != "applied" || row["response_json"] != body {
		return ErrDurableStateInvalid
	}
	return nil
}
