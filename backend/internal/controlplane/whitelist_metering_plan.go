package controlplane

import (
	"context"
	"sort"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	whiteListMeteringUnitGBDecimal           = "GB_DECIMAL"
	whiteListMeteringBasisUplinkPlusDownlink = "UPLINK_PLUS_DOWNLINK"
	whiteListMeteringPriceFree               = "FREE"
	whiteListMeteringPriceGlobal             = "GLOBAL"
)

// WhiteListMeteringPlan is the current durable binding used by the usage
// collector. An origin is usable only with its exact desired action and fresh
// receipt; every managed route is bound to its current prepaid access period.
type WhiteListMeteringPlan struct {
	Origins []WhiteListMeteringOrigin
	Routes  []WhiteListMeteringRoute
}

type WhiteListMeteringOrigin struct {
	Origin  WhiteListOrigin
	Desired WhiteListSidecarDesired
	Receipt WhiteListSidecarReceipt
	// A missing full initial debit must not enter the generic baseline adapter.
	// This is per origin/boot, not a route-global or billing-period reset flag.
	PendingFirstCumulativeUsers []string
}

type WhiteListMeteringRoute struct {
	ManagedEmail string
	ExitID       string
	Entitlement  WhiteListEntitlement
	Policy       WhiteListMeteringPolicy
}

type WhiteListMeteringPolicy struct {
	BillingPeriodID    string
	PeriodStartsAtUnix int64
	PeriodEndsAtUnix   int64
	Unit               string
	Basis              string
	IncludedBytes      uint64
	SoftLimitBytes     uint64
	HardLimitBytes     uint64
	GraceBytes         uint64
	PriceMode          string
	PriceSource        string
	Currency           string
	MinorUnitsPerUnit  uint64
}

// WhiteListMeteringPlan returns only one internally consistent generation.
// The FREE price is intentional: GB credit was paid for at top-up, so usage
// consumes the byte balance without charging money a second time.
func (s *Service) WhiteListMeteringPlan(ctx context.Context) (WhiteListMeteringPlan, error) {
	empty := WhiteListMeteringPlan{
		Origins: []WhiteListMeteringOrigin{},
		Routes:  []WhiteListMeteringRoute{},
	}
	if s == nil || s.store == nil || s.store.db == nil || s.store.secrets == nil || s.clock == nil || ctx == nil {
		return WhiteListMeteringPlan{}, ErrUnavailable
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return WhiteListMeteringPlan{}, err
	}
	if len(state.origins) == 0 {
		return empty, nil
	}

	desired := make([]WhiteListSidecarDesired, 0, len(state.origins))
	receiptReads := make([]rqlite.Statement, 0, len(state.origins))
	var canonical WhiteListSidecarDesired
	for index, origin := range state.origins {
		current, ok := state.previous[origin.OriginID]
		if !ok || current.OriginID != origin.OriginID || current.NodeID != origin.NodeID ||
			current.ReleaseID != origin.ReleaseID || current.ProfileID != origin.ProfileID ||
			current.PresetID != origin.PresetID || current.ConfigDigest != origin.ConfigDigest ||
			!whiteListMeteringManagedUsersCanonical(current.ManagedUsers) {
			return WhiteListMeteringPlan{}, ErrUnavailable
		}
		if index == 0 {
			canonical = current
		} else if current.ReleaseID != canonical.ReleaseID || current.ProfileID != canonical.ProfileID ||
			current.PresetID != canonical.PresetID || current.ExitID != canonical.ExitID ||
			current.Generation != canonical.Generation ||
			current.ManagedUserSetDigest != canonical.ManagedUserSetDigest ||
			!whiteListStringsEqual(current.ManagedUsers, canonical.ManagedUsers) {
			return WhiteListMeteringPlan{}, ErrUnavailable
		}
		desired = append(desired, current)
		receiptReads = append(receiptReads, whiteListSidecarReceiptRead(current.Action.ActionKey))
	}

	now := s.clock.Now()
	receiptResults, err := s.store.db.QueryLinearizable(ctx, receiptReads...)
	if err != nil || len(receiptResults) != len(receiptReads) {
		return WhiteListMeteringPlan{}, ErrUnavailable
	}
	plan := WhiteListMeteringPlan{
		Origins: make([]WhiteListMeteringOrigin, 0, len(state.origins)),
		Routes:  make([]WhiteListMeteringRoute, 0, len(canonical.ManagedUsers)),
	}
	for index, origin := range state.origins {
		receipt, receiptErr := whiteListSidecarReceiptFromResults(receiptResults[index : index+1])
		if receiptErr != nil || ValidateWhiteListSidecarReceipt(desired[index], receipt.XrayProcessBootID, receipt, now) != nil {
			return WhiteListMeteringPlan{}, ErrUnavailable
		}
		meteringOrigin := WhiteListMeteringOrigin{
			Origin: origin, Desired: cloneWhiteListMeteringDesired(desired[index]), Receipt: receipt,
			PendingFirstCumulativeUsers: []string{},
		}
		for _, email := range desired[index].ManagedUsers {
			entitlementID, exitID, valid := whiteListMeteringManagedRouteIdentity(email)
			observedOrigin := whiteListObservedOrigin{origin: origin, desired: desired[index], receipt: receipt}
			row, admissionErr := s.whiteListAdmissionRow(ctx, entitlementID, exitID, observedOrigin)
			period, _ := rowString(row, "billing_period_id")
			admitted, _ := rowInt64(row, "admitted_at_unix")
			if !valid || admissionErr != nil {
				meteringOrigin.PendingFirstCumulativeUsers = append(meteringOrigin.PendingFirstCumulativeUsers, email)
				continue
			}
			if _, proofErr := s.whiteListOriginAccountedThrough(ctx, entitlementID, exitID, period, admitted, observedOrigin); proofErr != nil {
				meteringOrigin.PendingFirstCumulativeUsers = append(meteringOrigin.PendingFirstCumulativeUsers, email)
			}
		}
		plan.Origins = append(plan.Origins, meteringOrigin)
	}

	entitlementIDs := make([]string, len(canonical.ManagedUsers))
	exitIDs := make([]string, len(canonical.ManagedUsers))
	periodReads := make([]rqlite.Statement, len(canonical.ManagedUsers))
	for index, managedEmail := range canonical.ManagedUsers {
		entitlementID, exitID, ok := whiteListMeteringManagedRouteIdentity(managedEmail)
		if !ok {
			return WhiteListMeteringPlan{}, ErrUnavailable
		}
		entitlementIDs[index], exitIDs[index] = entitlementID, exitID
		periodReads[index] = whiteListMeteringPeriodRead(entitlementID, now.Unix())
	}
	if len(periodReads) == 0 {
		return plan, nil
	}
	periodResults, err := s.store.db.QueryLinearizable(ctx, periodReads...)
	if err != nil || len(periodResults) != len(periodReads) {
		return WhiteListMeteringPlan{}, ErrUnavailable
	}
	for index, entitlementID := range entitlementIDs {
		row, ok := firstRow(periodResults[index : index+1])
		if !ok || len(periodResults[index].Rows) != 1 {
			return WhiteListMeteringPlan{}, ErrUnavailable
		}
		persistedEntitlementID, entitlementOK := rowString(row, "entitlement_id")
		accountID, accountOK := rowString(row, "customer_id")
		periodID, periodOK := rowString(row, "period_id")
		startsAtUnix, startsOK := rowInt64(row, "starts_at_unix")
		endsAtUnix, endsOK := rowInt64(row, "ends_at_unix")
		includedGrantBytes, includedOK := rowInt64(row, "included_grant_bytes")
		if !entitlementOK || persistedEntitlementID != entitlementID || !accountOK || !validAccountID(accountID) ||
			!periodOK || strings.TrimSpace(periodID) == "" || !startsOK || !endsOK ||
			startsAtUnix < 0 || endsAtUnix <= now.Unix() || startsAtUnix > now.Unix() ||
			!includedOK || includedGrantBytes != 0 {
			return WhiteListMeteringPlan{}, ErrUnavailable
		}
		material, materialErr := s.whiteListClientMaterial(ctx, entitlementID, exitIDs[index])
		if materialErr != nil {
			return WhiteListMeteringPlan{}, materialErr
		}
		entitlement, entitlementErr := whiteListEntitlementFromPersistedIdentity(accountID, entitlementID)
		if entitlementErr != nil {
			return WhiteListMeteringPlan{}, ErrUnavailable
		}
		entitlement, entitlementErr = entitlement.Activate(
			canonical.ProfileID,
			canonical.PresetID,
			canonical.ReleaseID,
			WhiteListCredential{
				ClientID: material.ClientID, ClientEncryption: material.ClientEncryption,
				ClientEncryptionRole:     material.ClientEncryptionRole,
				ClientEncryptionProofRef: material.ClientEncryptionProofRef,
			},
		)
		if entitlementErr != nil {
			return WhiteListMeteringPlan{}, ErrUnavailable
		}
		plan.Routes = append(plan.Routes, WhiteListMeteringRoute{
			ManagedEmail: canonical.ManagedUsers[index],
			ExitID:       exitIDs[index],
			Entitlement:  entitlement,
			Policy: WhiteListMeteringPolicy{
				BillingPeriodID: periodID, PeriodStartsAtUnix: startsAtUnix, PeriodEndsAtUnix: endsAtUnix,
				Unit: whiteListMeteringUnitGBDecimal, Basis: whiteListMeteringBasisUplinkPlusDownlink,
				PriceMode: whiteListMeteringPriceFree, PriceSource: whiteListMeteringPriceGlobal,
			},
		})
	}
	return plan, nil
}

func whiteListMeteringManagedUsersCanonical(users []string) bool {
	if !sort.StringsAreSorted(users) {
		return false
	}
	for index := 1; index < len(users); index++ {
		if users[index-1] == users[index] {
			return false
		}
	}
	return true
}

func whiteListMeteringManagedRouteIdentity(managedEmail string) (string, string, bool) {
	if !strings.HasPrefix(managedEmail, "wl:") {
		return "", "", false
	}
	value := strings.TrimPrefix(managedEmail, "wl:")
	separator := strings.LastIndexByte(value, ':')
	if separator <= 0 || separator == len(value)-1 {
		return "", "", false
	}
	entitlementID, exitID := value[:separator], value[separator+1:]
	valid := validEntitlementID(entitlementID) && routeCredentialExit(exitID) &&
		whiteListManagedEmail(entitlementID, exitID) == managedEmail
	return entitlementID, exitID, valid
}

func whiteListMeteringEntitlementID(managedEmail, exitID string) (string, bool) {
	entitlementID, managedExitID, ok := whiteListMeteringManagedRouteIdentity(managedEmail)
	return entitlementID, ok && managedExitID == exitID
}

func whiteListMeteringPeriodRead(entitlementID string, nowUnix int64) rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT identity.entitlement_id,identity.customer_id,
period.period_id,period.starts_at_unix,period.ends_at_unix,period.included_grant_bytes
FROM whitelist_entitlement_identities AS identity
JOIN whitelist_billing_periods AS period ON period.entitlement_id=identity.entitlement_id
WHERE ` + whiteListPeriodAuthoritySQL + ` AND identity.entitlement_id=? AND period.starts_at_unix<=? AND ?<period.ends_at_unix
ORDER BY period.period_ordinal`, Args: []any{entitlementID, nowUnix, nowUnix}}
}

func cloneWhiteListMeteringDesired(desired WhiteListSidecarDesired) WhiteListSidecarDesired {
	desired.StaticUsers = append([]string(nil), desired.StaticUsers...)
	desired.ManagedUsers = append([]string(nil), desired.ManagedUsers...)
	desired.PayloadJSON = append([]byte(nil), desired.PayloadJSON...)
	desired.Action.Request = append([]byte(nil), desired.Action.Request...)
	desired.Action.ReplayRequest = append([]byte(nil), desired.Action.ReplayRequest...)
	return desired
}
