package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/shadowbilling"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
)

const (
	runtimeWhiteListMeteringInterval   = 2 * time.Second
	runtimeWhiteListMeteringPassBudget = 5 * time.Second
)

var errRuntimeWhiteListMeteringUnavailable = errors.New("white-list metering runtime is unavailable")

type runtimeWhiteListUsageLookup interface {
	LookupUsage(context.Context, string) (sidecaragentclient.UsageSnapshot, error)
}

type runtimeWhiteListLeaseControlPlane interface {
	WhiteListUseLeaseTargets(context.Context) (map[string]string, error)
	AuthorizeWhiteListFinalReceipt(context.Context, string, sidecaragentclient.ManagedFinalReceipt) (controlplane.WhiteListFinalReceiptAuthorization, error)
	WhiteListUseLeaseAuthorizations(context.Context, controlplane.WhiteListMeteringPlan, func(string) (controlplane.ExternalActionSender, bool)) (controlplane.WhiteListUseLeaseAuthorization, error)
}

type runtimeWhiteListLeaseSender interface {
	LookupFinalReceipts(context.Context) (sidecaragentclient.FinalReceiptPage, error)
	AckFinalReceipts(context.Context, []sidecaragentclient.FinalReceiptACK) error
	PostUseLease(context.Context, sidecaragentclient.UseLeaseRequest) (sidecaragentclient.UseLeaseResponse, error)
}

type runtimeWhiteListFinalStore interface {
	ApplyCommercialFinalReceipt(context.Context, controlplane.WhiteListFinalReceiptAuthorization, shadowbilling.CommercialDebiter) (shadowbilling.DurableResult, error)
}

type runtimeWhiteListMeteringControlPlane interface {
	shadowbilling.CommercialDebiter
	WhiteListMeteringPlan(context.Context) (controlplane.WhiteListMeteringPlan, error)
	WhiteListMeteringAdmissionCandidates(context.Context) ([]controlplane.WhiteListMeteringAdmissionCandidate, error)
	AuthorizeWhiteListMeteringAdmission(context.Context, string, string, controlplane.WhiteListAdmissionReserve) error
	RecordWhiteListOriginObservation(context.Context, controlplane.WhiteListOriginObservation) error
	EnsureWhiteListMeteringBootstrap(context.Context, string, func(string) (controlplane.ExternalActionSender, bool)) error
	ReconcileWhiteListSidecarIntents(
		context.Context, string, func(string) (controlplane.ExternalActionSender, bool),
	) error
}

type runtimeWhiteListMeteringStore interface {
	ApplyCommercialFirstCumulative(
		context.Context, shadowbilling.CommercialOrderedUsageEvent, shadowbilling.Policy,
		shadowbilling.CommercialDebiter,
	) (shadowbilling.DurableResult, error)
	PendingCommercialDebitEntitlementIDs(context.Context) ([]string, error)
	DrainCommercialDebits(context.Context, string, shadowbilling.CommercialDebiter) error
	EnsureCommercialProducerCursor(
		context.Context, shadowbilling.CommercialMeterSource, int64,
	) (shadowbilling.CommercialProducerCursor, error)
	ApplyCommercialOrdered(
		context.Context, shadowbilling.CommercialOrderedUsageEvent, shadowbilling.Policy,
		shadowbilling.CommercialDebiter,
	) (shadowbilling.DurableResult, error)
}

type runtimeWhiteListMeteringCollector struct {
	control          runtimeWhiteListMeteringControlPlane
	store            runtimeWhiteListMeteringStore
	workerID         string
	senders          map[string]controlplane.ExternalActionSender
	startupRecovered bool
	reconcileNeeded  bool
	reserves         runtimeWhiteListReserveProvider
}

func newRuntimeWhiteListMeteringStore(database rqlite.RQLite) (*shadowbilling.DurableStore, error) {
	return shadowbilling.NewDurableStore(database)
}

func runRQLiteBackground(
	ctx context.Context,
	renewal whiteListRenewalReconciler,
	sidecar whiteListSidecarIntentReconciler,
	metering runtimeWhiteListMeteringControlPlane,
	meteringStore runtimeWhiteListMeteringStore,
	workerID string,
	senders map[string]controlplane.ExternalActionSender,
	meteringEnabled bool,
	reserves runtimeWhiteListReserveProvider,
) {
	if ctx == nil || renewal == nil {
		return
	}
	var workers sync.WaitGroup
	if meteringEnabled && metering != nil && meteringStore != nil &&
		strings.TrimSpace(workerID) != "" && len(senders) > 0 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runRuntimeWhiteListMetering(ctx, &runtimeWhiteListMeteringCollector{
				control: metering, store: meteringStore, workerID: workerID, senders: senders,
				reserves: reserves,
			}, runtimeWhiteListMeteringInterval)
		}()
	}
	runRQLiteReconcilers(ctx, renewal, sidecar, workerID, senders, runtimeWhiteListRenewalInterval)
	workers.Wait()
}

func runRuntimeWhiteListMetering(
	ctx context.Context,
	collector *runtimeWhiteListMeteringCollector,
	interval time.Duration,
) {
	if ctx == nil || collector == nil || collector.control == nil || collector.store == nil ||
		strings.TrimSpace(collector.workerID) == "" || len(collector.senders) == 0 || interval <= 0 {
		return
	}
	runPass := func() {
		if err := collector.runPass(ctx); err != nil && ctx.Err() == nil {
			log.Printf("white-list metering reconciliation deferred: %v", err)
		}
	}
	runPass()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runPass()
		}
	}
}

func (collector *runtimeWhiteListMeteringCollector) runPass(ctx context.Context) (runErr error) {
	started := time.Now()
	// Cooperative operation bounds, not proof of the live sampling/revoke SLO.
	// Recovery must keep time to reconcile even when sampling exhausts its budget.
	reconcileContext, cancelReconcile := context.WithDeadline(ctx, started.Add(runtimeWhiteListMeteringPassBudget))
	defer cancelReconcile()
	ctx, cancelSampling := context.WithDeadline(reconcileContext, started.Add(runtimeWhiteListMeteringInterval))
	defer cancelSampling()
	collector.reconcileNeeded = true
	defer func() {
		if !collector.reconcileNeeded {
			return
		}
		if err := collector.reconcile(reconcileContext); err != nil {
			if runErr == nil {
				runErr = errRuntimeWhiteListMeteringUnavailable
			}
			return
		}
		collector.reconcileNeeded = false
	}()

	if !collector.startupRecovered {
		entitlementIDs, err := collector.store.PendingCommercialDebitEntitlementIDs(ctx)
		if err != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		for _, entitlementID := range entitlementIDs {
			collector.reconcileNeeded = true
			if err := collector.store.DrainCommercialDebits(ctx, entitlementID, collector.control); err != nil {
				return errRuntimeWhiteListMeteringUnavailable
			}
		}
		collector.startupRecovered = true
	}
	resolve := func(nodeID string) (controlplane.ExternalActionSender, bool) {
		sender, ok := collector.senders[nodeID]
		return sender, ok && sender != nil
	}
	leaseControl, leaseEnabled := collector.control.(runtimeWhiteListLeaseControlPlane)
	if leaseEnabled {
		if err := collector.drainFinalReceipts(ctx, leaseControl); err != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
	}
	if err := collector.control.EnsureWhiteListMeteringBootstrap(ctx, collector.workerID, resolve); err != nil {
		return errRuntimeWhiteListMeteringUnavailable
	}
	plan, err := collector.control.WhiteListMeteringPlan(ctx)
	if err != nil {
		return errRuntimeWhiteListMeteringUnavailable
	}
	routes := make(map[string]controlplane.WhiteListMeteringRoute, len(plan.Routes))
	for _, route := range plan.Routes {
		if route.ManagedEmail == "" {
			return errRuntimeWhiteListMeteringUnavailable
		}
		if _, duplicate := routes[route.ManagedEmail]; duplicate {
			return errRuntimeWhiteListMeteringUnavailable
		}
		routes[route.ManagedEmail] = route
	}
	snapshots := make(map[string]sidecaragentclient.UsageSnapshot, len(plan.Origins))
	for _, origin := range plan.Origins {
		sender, ok := collector.senders[origin.Origin.NodeID]
		if !ok || sender == nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		lookup, ok := sender.(runtimeWhiteListUsageLookup)
		if !ok {
			return errRuntimeWhiteListMeteringUnavailable
		}
		snapshot, lookupErr := lookup.LookupUsage(ctx, origin.Desired.Action.ActionKey)
		if lookupErr != nil || !runtimeWhiteListUsageReceiptMatches(origin.Receipt, snapshot.Receipt) {
			return errRuntimeWhiteListMeteringUnavailable
		}
		if leaseEnabled {
			if snapshot.LeaseChallenge == nil || snapshot.PendingUseLease != nil || len(snapshot.FinalReceipts) > 0 || snapshot.HasMoreFinalReceipts {
				return errRuntimeWhiteListMeteringUnavailable
			}
			if _, ok := sender.(runtimeWhiteListLeaseSender); !ok {
				return errRuntimeWhiteListMeteringUnavailable
			}
		}
		snapshots[origin.Origin.OriginID] = snapshot
		if len(snapshot.Users)+len(snapshot.UnavailableUsers) != len(routes) {
			return errRuntimeWhiteListMeteringUnavailable
		}
		seen := make(map[string]struct{}, len(routes))
		for _, email := range snapshot.UnavailableUsers {
			if _, ok := routes[email]; !ok {
				return errRuntimeWhiteListMeteringUnavailable
			}
			if _, duplicate := seen[email]; duplicate {
				return errRuntimeWhiteListMeteringUnavailable
			}
			seen[email] = struct{}{}
		}
		available := make([]string, 0, len(snapshot.Users))
		for _, user := range snapshot.Users {
			_, ok := routes[user.Email]
			if !ok {
				return errRuntimeWhiteListMeteringUnavailable
			}
			if _, duplicate := seen[user.Email]; duplicate {
				return errRuntimeWhiteListMeteringUnavailable
			}
			seen[user.Email] = struct{}{}
			available = append(available, user.Email)
		}
		if len(seen) != len(routes) {
			return errRuntimeWhiteListMeteringUnavailable
		}
		if err := collector.control.RecordWhiteListOriginObservation(ctx, controlplane.WhiteListOriginObservation{
			Receipt: origin.Receipt, SampledAt: snapshot.SampledAt,
			AvailableUsers: available, UnavailableUsers: snapshot.UnavailableUsers,
		}); err != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		for _, user := range snapshot.Users {
			index := sort.SearchStrings(origin.PendingFirstCumulativeUsers, user.Email)
			firstCumulative := index < len(origin.PendingFirstCumulativeUsers) && origin.PendingFirstCumulativeUsers[index] == user.Email
			if err := collector.applyUser(ctx, origin, routes[user.Email], snapshot.SampledAt, user, firstCumulative); err != nil {
				return errRuntimeWhiteListMeteringUnavailable
			}
		}
	}
	if err := collector.authorizeAdmissions(ctx); err != nil {
		return err
	}
	if !leaseEnabled {
		return nil
	}
	authorization, err := leaseControl.WhiteListUseLeaseAuthorizations(ctx, plan, resolve)
	if err != nil {
		return errRuntimeWhiteListMeteringUnavailable
	}
	// One common conservative budget is anchored to each agent's own earlier
	// read start. Backend wall time is never compared with remote BOOTTIME.
	for _, origin := range plan.Origins {
		if ctx.Err() != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		request, err := sidecaragentclient.NewUseLeaseRequest(snapshots[origin.Origin.OriginID], authorization.FreshFor, authorization.Emails)
		if err != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		sender := collector.senders[origin.Origin.NodeID].(runtimeWhiteListLeaseSender)
		if _, err := sender.PostUseLease(ctx, request); err != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
	}
	return nil
}

// Drain retained evidence before requesting a new usage nonce, including
// removed users and stale readiness. Exact pending bodies survive backend
// restart in the agent journal; they are replayed verbatim, never renewed.
func (collector *runtimeWhiteListMeteringCollector) drainFinalReceipts(ctx context.Context, control runtimeWhiteListLeaseControlPlane) error {
	store, ok := collector.store.(runtimeWhiteListFinalStore)
	if !ok {
		return errRuntimeWhiteListMeteringUnavailable
	}
	targets, err := control.WhiteListUseLeaseTargets(ctx)
	if err != nil {
		return err
	}
	origins := make([]string, 0, len(targets))
	for origin := range targets {
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	for _, origin := range origins {
		sender, ok := collector.senders[targets[origin]].(runtimeWhiteListLeaseSender)
		if !ok {
			return errRuntimeWhiteListMeteringUnavailable
		}
		drained := false
		for pageNumber := 0; pageNumber < 130; pageNumber++ {
			if ctx.Err() != nil {
				return errRuntimeWhiteListMeteringUnavailable
			}
			page, err := sender.LookupFinalReceipts(ctx)
			if err != nil || page.Schema != 2 || len(page.FinalReceipts) > 32 || page.HasMoreFinalReceipts && len(page.FinalReceipts) == 0 {
				return errRuntimeWhiteListMeteringUnavailable
			}
			ack := make([]sidecaragentclient.FinalReceiptACK, 0, len(page.FinalReceipts))
			for _, final := range page.FinalReceipts {
				if final.OriginID != origin {
					return errRuntimeWhiteListMeteringUnavailable
				}
				authorization, err := control.AuthorizeWhiteListFinalReceipt(ctx, targets[origin], final)
				if err != nil || !authorization.Verified() {
					return errRuntimeWhiteListMeteringUnavailable
				}
				if !authorization.Unused() {
					if _, err := store.ApplyCommercialFinalReceipt(ctx, authorization, collector.control); err != nil {
						return errRuntimeWhiteListMeteringUnavailable
					}
				}
				ack = append(ack, sidecaragentclient.FinalReceiptACK{ReceiptID: final.ReceiptID, ProofSHA256: final.ProofSHA256})
			}
			if len(ack) > 0 {
				if err := sender.AckFinalReceipts(ctx, ack); err != nil {
					return errRuntimeWhiteListMeteringUnavailable
				}
				continue
			}
			if page.PendingUseLease != nil {
				if _, err := sender.PostUseLease(ctx, *page.PendingUseLease); err != nil {
					return errRuntimeWhiteListMeteringUnavailable
				}
				continue
			}
			drained = true
			break
		}
		// The bounded loop cannot authorize use on an unproven empty backlog.
		if !drained {
			return errRuntimeWhiteListMeteringUnavailable
		}
	}
	return nil
}

// Run only after every authenticated Origin observation and debit succeeded.
// Candidate discovery must not depend on already provisioned managed users.
func (collector *runtimeWhiteListMeteringCollector) authorizeAdmissions(ctx context.Context) error {
	if collector.reserves == nil {
		return nil
	}
	reserves, err := collector.reserves(ctx)
	if err != nil {
		return errRuntimeWhiteListMeteringUnavailable
	}
	candidates, err := collector.control.WhiteListMeteringAdmissionCandidates(ctx)
	if err != nil {
		return errRuntimeWhiteListMeteringUnavailable
	}
	var admissionErr error
	for _, candidate := range candidates {
		reserve, measured := reserves[candidate.ExitID]
		if !measured {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		if err := collector.control.AuthorizeWhiteListMeteringAdmission(ctx, candidate.EntitlementID, candidate.ExitID, reserve); err != nil {
			// An ineligible account must not starve the remaining eligible accounts.
			admissionErr = errRuntimeWhiteListMeteringUnavailable
		}
	}
	return admissionErr
}

func (collector *runtimeWhiteListMeteringCollector) applyUser(
	ctx context.Context,
	origin controlplane.WhiteListMeteringOrigin,
	route controlplane.WhiteListMeteringRoute,
	sampledAt time.Time,
	user sidecaragentclient.UsageUser,
	firstCumulative bool,
) error {
	sampledAtUnix := sampledAt.Unix()
	if sampledAtUnix <= 0 || sampledAtUnix < route.Policy.PeriodStartsAtUnix ||
		sampledAtUnix >= route.Policy.PeriodEndsAtUnix || route.ExitID == "" || user.Email != route.ManagedEmail {
		return errRuntimeWhiteListMeteringUnavailable
	}
	policy, err := runtimeWhiteListMeteringPolicy(route)
	if err != nil {
		return err
	}
	baseXrayIdentity, ok := route.Entitlement.XrayIdentity()
	if !ok {
		return errRuntimeWhiteListMeteringUnavailable
	}
	physicalSource := shadowbilling.CommercialMeterSource{
		OriginID: origin.Origin.OriginID, ExitID: route.ExitID,
		CounterSourceID:   "xray-api:" + origin.Origin.OriginID + ":" + route.ExitID,
		XrayProcessBootID: origin.Receipt.XrayProcessBootID,
		RouteXrayIdentity: route.ManagedEmail,
	}
	cursor, err := collector.store.EnsureCommercialProducerCursor(ctx, physicalSource, sampledAtUnix)
	if err != nil {
		return err
	}
	if firstCumulative && cursor.NextSampleSequence != 1 {
		// A committed first interval can still await its balance receipt. Drain
		// it before the next plan proves first accounting; never rebase or rearm.
		return collector.store.DrainCommercialDebits(ctx, route.Entitlement.EntitlementID(), collector.control)
	}
	eventID := runtimeWhiteListMeteringEventID(cursor.MeterEpoch, route.ManagedEmail, cursor.NextSampleSequence)
	collector.reconcileNeeded = true
	event := shadowbilling.CommercialOrderedUsageEvent{
		OrderedUsageEvent: shadowbilling.OrderedUsageEvent{
			UsageEvent: shadowbilling.UsageEvent{
				EventID: eventID, InstanceID: origin.Origin.OriginID, MeterEpoch: cursor.MeterEpoch,
				XrayIdentity: baseXrayIdentity, UplinkBytes: user.UplinkBytes, DownlinkBytes: user.DownlinkBytes,
			},
			CounterGeneration: 1, SampleSequence: cursor.NextSampleSequence,
		},
		Source: cursor.Source, SampledAtUnix: sampledAtUnix,
	}
	if firstCumulative {
		_, err = collector.store.ApplyCommercialFirstCumulative(ctx, event, policy, collector.control)
	} else {
		_, err = collector.store.ApplyCommercialOrdered(ctx, event, policy, collector.control)
	}
	if err != nil {
		return err
	}
	if err := collector.store.DrainCommercialDebits(ctx, route.Entitlement.EntitlementID(), collector.control); err != nil {
		return err
	}
	return nil
}

func (collector *runtimeWhiteListMeteringCollector) reconcile(ctx context.Context) error {
	resolve := func(nodeID string) (controlplane.ExternalActionSender, bool) {
		sender, ok := collector.senders[nodeID]
		return sender, ok && sender != nil
	}
	return collector.control.ReconcileWhiteListSidecarIntents(ctx, collector.workerID, resolve)
}

func runtimeWhiteListMeteringPolicy(route controlplane.WhiteListMeteringRoute) (shadowbilling.Policy, error) {
	policy := route.Policy
	if policy.BillingPeriodID == "" || policy.Unit != string(shadowbilling.UnitGBDecimal) ||
		policy.Basis != string(shadowbilling.BasisUplinkPlusDownlink) || policy.IncludedBytes != 0 ||
		policy.SoftLimitBytes != 0 || policy.HardLimitBytes != 0 || policy.GraceBytes != 0 ||
		policy.PriceMode != string(shadowbilling.PriceFree) || policy.PriceSource != string(shadowbilling.PriceGlobal) ||
		policy.Currency != "" || policy.MinorUnitsPerUnit != 0 {
		return shadowbilling.Policy{}, errRuntimeWhiteListMeteringUnavailable
	}
	return shadowbilling.NewPolicy(route.Entitlement, shadowbilling.PolicySpec{
		BillingPeriodID: policy.BillingPeriodID,
		Unit:            shadowbilling.UnitGBDecimal, Basis: shadowbilling.BasisUplinkPlusDownlink,
		Prices: shadowbilling.PriceOptions{Global: &shadowbilling.Price{Mode: shadowbilling.PriceFree}},
	})
}

func runtimeWhiteListUsageReceiptMatches(
	want controlplane.WhiteListSidecarReceipt,
	got sidecaragentclient.Receipt,
) bool {
	return want.ActionKey == got.ActionKey && want.OriginID == got.OriginID &&
		want.ReleaseID == got.ReleaseID && want.XrayProcessBootID == got.XrayProcessBootID &&
		want.ConfigDigest == got.ConfigDigest && want.DesiredGeneration == got.DesiredGeneration &&
		want.ManagedUserSetDigest == got.ManagedUserSetDigest && want.AppliedAt.Equal(got.AppliedAt) &&
		want.ExpiresAt.Equal(got.ExpiresAt)
}

func runtimeWhiteListMeteringEventID(meterEpoch, routeIdentity string, sequence uint64) string {
	digest := sha256.Sum256([]byte(
		"maestro-whitelist-usage-event-v1\x00" + meterEpoch + "\x00" + routeIdentity + "\x00" +
			strconv.FormatUint(sequence, 10),
	))
	return "wl-usage-" + hex.EncodeToString(digest[:])
}
