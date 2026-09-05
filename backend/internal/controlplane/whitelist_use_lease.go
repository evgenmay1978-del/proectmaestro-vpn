package controlplane

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
)

type WhiteListUseLeaseAuthorization struct {
	Emails   []string
	FreshFor time.Duration
}

// A value can only be issued after authenticating the retained agent proof
// against historical desired/receipt and immutable admission rows. Persisting
// this evidence does not accept a metering sequence or acknowledge any bytes.
type WhiteListFinalReceiptAuthorization struct {
	verified bool
	final    sidecaragentclient.ManagedFinalReceipt
	route    WhiteListMeteringRoute
}

func (a WhiteListFinalReceiptAuthorization) Verified() bool { return a.verified }
func (a WhiteListFinalReceiptAuthorization) Unused() bool {
	return a.verified && a.final.Receipt.State == "fenced_unused"
}
func (a WhiteListFinalReceiptAuthorization) EventID() string {
	if !a.verified {
		return ""
	}
	return "wl-final:" + a.final.ReceiptID
}
func (a WhiteListFinalReceiptAuthorization) Route() WhiteListMeteringRoute { return a.route }
func (a WhiteListFinalReceiptAuthorization) Receipt() sidecaragentclient.ManagedFinalReceipt {
	v := a.final
	if v.Receipt.Uplink != nil {
		x := *v.Receipt.Uplink
		v.Receipt.Uplink = &x
	}
	if v.Receipt.Downlink != nil {
		x := *v.Receipt.Downlink
		v.Receipt.Downlink = &x
	}
	return v
}

func (s *Service) AuthorizeWhiteListFinalReceipt(ctx context.Context, nodeID string, final sidecaragentclient.ManagedFinalReceipt) (WhiteListFinalReceiptAuthorization, error) {
	closed := WhiteListFinalReceiptAuthorization{}
	if s == nil || s.store == nil || s.store.db == nil || s.clock == nil || ctx == nil || nodeID == "" || sidecaragentclient.ValidateManagedFinalReceipt(final) != nil {
		return closed, ErrUnavailable
	}
	observed, err := time.Parse(time.RFC3339Nano, final.Receipt.ObservedAt)
	if err != nil || observed.After(s.clock.Now()) {
		return closed, ErrUnavailable
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT origin_id,node_id,release_id,profile_id,preset_id,exit_id,config_digest,managed_user_set_digest,desired_sha256,action_type,action_key,desired_generation,payload_json FROM whitelist_sidecar_desired WHERE action_key=?`, Args: []any{final.ActionKey}}, whiteListSidecarReceiptRead(final.ActionKey))
	if err != nil || len(results) != 2 || len(results[0].Rows) != 1 || len(results[1].Rows) > 1 {
		return closed, ErrUnavailable
	}
	desired, err := whiteListRuntimeDesiredFromRow(results[0].Rows[0])
	if err != nil {
		return closed, err
	}
	if desired.NodeID != nodeID || desired.OriginID != final.OriginID || desired.ReleaseID != final.ReleaseID || desired.Generation != final.DesiredGeneration || desired.ConfigDigest != final.Control.ConfigDigest || desired.ManagedUserSetDigest != final.ManagedUserSetDigest || !whiteListContainsUser(desired.ManagedUsers, final.Control.Email) {
		return closed, ErrUnavailable
	}
	// A bootstrap fence runs before AddUser and before the new desired receipt.
	// Its authenticated never-used proof has no bytes or cutoff to settle; the
	// immutable desired action binds its node and exact physical runtime proof.
	// Actual counters still require successful provisioning and its lower time
	// bound. Neither branch grants use or supplies a synthetic zero sample.
	if final.Receipt.State != "fenced_unused" {
		receipt, err := whiteListSidecarReceiptFromResults(results[1:])
		if err != nil {
			return closed, err
		}
		if receipt.OriginID != final.OriginID || receipt.ReleaseID != final.ReleaseID || receipt.DesiredGeneration != final.DesiredGeneration || receipt.ManagedUserSetDigest != final.ManagedUserSetDigest || receipt.ConfigDigest != final.Control.ConfigDigest || receipt.XrayProcessBootID != final.Control.BootID || observed.Before(receipt.AppliedAt) {
			return closed, ErrUnavailable
		}
	}
	entitlementID, ok := whiteListMeteringEntitlementID(final.Control.Email, desired.ExitID)
	if !ok {
		return closed, ErrUnavailable
	}
	identityResults, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT customer_id FROM whitelist_entitlement_identities WHERE entitlement_id=?`, Args: []any{entitlementID}})
	if err != nil || len(identityResults) != 1 || len(identityResults[0].Rows) != 1 {
		return closed, ErrUnavailable
	}
	accountID, ok := rowString(identityResults[0].Rows[0], "customer_id")
	if !ok {
		return closed, ErrUnavailable
	}
	entitlement, err := whiteListEntitlementFromPersistedIdentity(accountID, entitlementID)
	if err != nil {
		return closed, ErrUnavailable
	}
	route := WhiteListMeteringRoute{ManagedEmail: final.Control.Email, ExitID: desired.ExitID, Entitlement: entitlement}
	if final.Receipt.State == "fenced" {
		periodResults, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT period.period_id,period.starts_at_unix,period.ends_at_unix,period.included_grant_bytes,admission.admitted_at_unix
FROM whitelist_first_use_admissions AS admission
JOIN whitelist_billing_periods AS period ON period.period_id=admission.billing_period_id AND period.entitlement_id=admission.entitlement_id
JOIN orders AS access_order ON access_order.order_id=period.access_order_id AND access_order.customer_id=? AND access_order.payment_state='confirmed' AND access_order.decision='confirmed' AND access_order.confirmed_at_unix IS NOT NULL
WHERE admission.entitlement_id=? AND admission.exit_id=? AND admission.origin_id=? AND admission.xray_process_boot_id=? AND admission.zero_start_authorized=1`, Args: []any{accountID, entitlementID, desired.ExitID, final.OriginID, final.Control.BootID}})
		if err != nil || len(periodResults) != 1 || len(periodResults[0].Rows) != 1 {
			return closed, ErrUnavailable
		}
		row := periodResults[0].Rows[0]
		period, periodOK := rowString(row, "period_id")
		start, startOK := rowInt64(row, "starts_at_unix")
		end, endOK := rowInt64(row, "ends_at_unix")
		included, includedOK := rowInt64(row, "included_grant_bytes")
		admitted, admittedOK := rowInt64(row, "admitted_at_unix")
		if !periodOK || period == "" || !startOK || !endOK || !includedOK || included != 0 || !admittedOK || start > admitted || admitted > observed.Unix() || observed.Unix() >= end {
			return closed, ErrUnavailable
		}
		material, err := s.whiteListClientMaterial(ctx, entitlementID, desired.ExitID)
		if err != nil {
			return closed, ErrUnavailable
		}
		entitlement, err = entitlement.Activate(desired.ProfileID, desired.PresetID, desired.ReleaseID, WhiteListCredential{ClientID: material.ClientID, ClientEncryption: material.ClientEncryption, ClientEncryptionRole: material.ClientEncryptionRole, ClientEncryptionProofRef: material.ClientEncryptionProofRef})
		if err != nil {
			return closed, ErrUnavailable
		}
		route.Entitlement = entitlement
		route.Policy = WhiteListMeteringPolicy{BillingPeriodID: period, PeriodStartsAtUnix: start, PeriodEndsAtUnix: end, Unit: whiteListMeteringUnitGBDecimal, Basis: whiteListMeteringBasisUplinkPlusDownlink, PriceMode: whiteListMeteringPriceFree, PriceSource: whiteListMeteringPriceGlobal}
	}
	body, err := json.Marshal(final)
	if err != nil {
		return closed, ErrUnavailable
	}
	_, err = s.store.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `INSERT OR IGNORE INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,response_json,created_at_unix,applied_at_unix) VALUES('whitelist-final-proof','accept-agent-fence',?,?,?,'accepted',?,'applied',?,?,?)`, Args: []any{final.ReceiptID, final.ProofSHA256, entitlementID, "wl-final-proof:" + final.ReceiptID, string(body), s.clock.Now().Unix(), s.clock.Now().Unix()}})
	// An unknown commit is resolved by exact immutable evidence, never by a
	// second operation or by accepting a changed proof for the same receipt ID.
	stored, readErr := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT request_hash,resource_id,response_json,status FROM idempotency_requests WHERE scope='whitelist-final-proof' AND command_type='accept-agent-fence' AND idempotency_key=?`, Args: []any{final.ReceiptID}})
	if readErr != nil || len(stored) != 1 || len(stored[0].Rows) != 1 {
		return closed, ErrUnavailable
	}
	row := stored[0].Rows[0]
	if row["request_hash"] != final.ProofSHA256 || row["resource_id"] != entitlementID || row["response_json"] != string(body) || row["status"] != "applied" {
		return closed, ErrConflict
	}
	closed = WhiteListFinalReceiptAuthorization{verified: true, final: final, route: route}
	closed.final = closed.Receipt()
	return closed, nil
}

// Historical targets remain discoverable independently of current receipt or
// paid-period readiness. Their retained final evidence must be settled before
// the collector can authorize another use lease.
func (s *Service) WhiteListUseLeaseTargets(ctx context.Context) (map[string]string, error) {
	if s == nil || s.store == nil || s.store.db == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT origin.origin_id,origin.node_id
FROM whitelist_sidecar_origins AS origin
WHERE EXISTS(SELECT 1 FROM whitelist_sidecar_desired AS desired WHERE desired.origin_id=origin.origin_id)
ORDER BY origin.origin_id`})
	if err != nil || len(results) != 1 {
		return nil, ErrUnavailable
	}
	targets := make(map[string]string, len(results[0].Rows))
	for _, row := range results[0].Rows {
		origin, originOK := rowString(row, "origin_id")
		node, nodeOK := rowString(row, "node_id")
		if !originOK || !nodeOK || origin == "" || node == "" || targets[origin] != "" {
			return nil, ErrUnavailable
		}
		targets[origin] = node
	}
	return targets, nil
}

// Call only after the collector persisted every exact Origin observation and
// applied its actual debit. This method independently rechecks that durable
// state with the same evaluator as public delivery. It cannot grant by token,
// refresh desired state, or manufacture an initial counter observation.
func (s *Service) WhiteListUseLeaseAuthorizations(ctx context.Context, plan WhiteListMeteringPlan, resolve func(string) (ExternalActionSender, bool)) (WhiteListUseLeaseAuthorization, error) {
	closed := WhiteListUseLeaseAuthorization{Emails: []string{}}
	if s == nil || s.clock == nil || ctx == nil || resolve == nil || len(plan.Origins) == 0 {
		return closed, ErrUnavailable
	}
	byOrigin := make(map[string]WhiteListMeteringOrigin, len(plan.Origins))
	for _, origin := range plan.Origins {
		if byOrigin[origin.Origin.OriginID].Origin.OriginID != "" {
			return closed, ErrUnavailable
		}
		byOrigin[origin.Origin.OriginID] = origin
	}
	now := s.clock.Now()
	until := now.Add(5 * time.Second)
	seen := map[string]bool{}
	for _, route := range plan.Routes {
		if seen[route.ManagedEmail] || route.ManagedEmail != whiteListManagedEmail(route.Entitlement.EntitlementID(), route.ExitID) {
			return closed, ErrUnavailable
		}
		seen[route.ManagedEmail] = true
		delivery, err := s.whiteListPublicationForEntitlement(ctx, route.Entitlement.EntitlementID(), now, resolve, false)
		if err != nil {
			return closed, err
		}
		if delivery.Decision.Verdict != WhiteListPublicationPublishable {
			continue
		}
		if delivery.ExitID != route.ExitID || len(delivery.desiredBindings) != len(byOrigin) {
			return closed, ErrUnavailable
		}
		for _, desired := range delivery.desiredBindings {
			origin, exists := byOrigin[desired.OriginID]
			if !exists || desired.Action.ActionKey != origin.Desired.Action.ActionKey || desired.Generation != origin.Desired.Generation ||
				desired.ConfigDigest != origin.Desired.ConfigDigest || desired.ManagedUserSetDigest != origin.Desired.ManagedUserSetDigest ||
				!whiteListContainsUser(desired.ManagedUsers, route.ManagedEmail) {
				return closed, ErrUnavailable
			}
		}
		freshUntil := time.Unix(delivery.Decision.FreshUntilUnix, 0)
		if freshUntil.Before(until) {
			until = freshUntil
		}
		closed.Emails = append(closed.Emails, route.ManagedEmail)
	}
	if ctx.Err() != nil {
		return WhiteListUseLeaseAuthorization{Emails: []string{}}, ErrUnavailable
	}
	remaining := until.Sub(s.clock.Now())
	if remaining <= 0 || remaining > 5*time.Second {
		return WhiteListUseLeaseAuthorization{Emails: []string{}}, ErrUnavailable
	}
	sort.Strings(closed.Emails)
	closed.FreshFor = remaining
	return closed, nil
}
