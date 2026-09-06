package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

var (
	errRuntimeWhiteListMeteringUnavailable = errors.New("white-list metering runtime is unavailable")
	errRuntimeWhiteListFreshLeaseNonce     = errors.New("white-list metering requires a fresh lease nonce")
)

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

type runtimeWhiteListByteBudgetControlPlane interface {
	AuthorizeWhiteListByteBudgetAdmission(context.Context, string, string, int64) error
	CompleteWhiteListByteBudgetFinal(context.Context, controlplane.WhiteListFinalReceiptAuthorization) error
}

type runtimeWhiteListByteBudgetRefillControlPlane interface {
	WhiteListByteBudgetRefillCandidates(context.Context, controlplane.WhiteListMeteringPlan, []controlplane.WhiteListMeteringAdmissionCandidate, int64) ([]controlplane.WhiteListMeteringAdmissionCandidate, error)
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
	byteBudgetBytes  int64
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
	byteBudgetBytes ...int64,
) {
	if ctx == nil || renewal == nil {
		return
	}
	var workers sync.WaitGroup
	var byteBudget int64
	if len(byteBudgetBytes) == 1 {
		byteBudget = byteBudgetBytes[0]
	}
	if meteringEnabled && metering != nil && meteringStore != nil &&
		strings.TrimSpace(workerID) != "" && len(senders) > 0 {
		// The collector orders receipt renewal, fresh counters and publication.
		// A second sidecar worker must not revoke between those durable steps.
		sidecar = nil
		workers.Add(1)
		go func() {
			defer workers.Done()
			runRuntimeWhiteListMetering(ctx, &runtimeWhiteListMeteringCollector{
				control: metering, store: meteringStore, workerID: workerID, senders: senders,
				reserves: reserves, byteBudgetBytes: byteBudget,
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
		err := collector.runPass(ctx)
		if errors.Is(err, errRuntimeWhiteListFreshLeaseNonce) && ctx.Err() == nil {
			// A completed deny-only rearm is expected to return HTTP 503 with a
			// verified fresh-nonce response. Start one new full pass immediately;
			// it drains final receipts before reading a new agent nonce.
			err = collector.runPass(ctx)
		}
		if err != nil && ctx.Err() == nil {
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
	stage := "startup recovery"
	passBudget, processingBudget := runtimeWhiteListMeteringPassBudget, runtimeWhiteListMeteringInterval
	if collector.byteBudgetBytes > 0 {
		// Prepaid byte ceilings bound forwarding independently of processing time.
		// Keep the original five-second observation and BOOTTIME lease deadlines;
		// this only lets durable recovery/accounting finish before cancellation.
		passBudget, processingBudget = 10*time.Second, 5*time.Second
	}
	// Cooperative operation bounds, not proof of the live sampling/revoke SLO.
	// Recovery must keep time to reconcile even when sampling exhausts its budget.
	reconcileContext, cancelReconcile := context.WithDeadline(ctx, started.Add(passBudget))
	defer cancelReconcile()
	ctx = reconcileContext
	collector.reconcileNeeded = true
	defer func() {
		if runErr != nil {
			runErr = fmt.Errorf("%s after %s: %w", stage, time.Since(started).Round(time.Millisecond), runErr)
		}
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
	stage = "final receipt drain"
	if leaseEnabled {
		if err := collector.drainFinalReceipts(ctx, leaseControl); err != nil {
			return fmt.Errorf("final receipt drain: %w", err)
		}
	}
	stage = "empty bootstrap"
	if err := collector.control.EnsureWhiteListMeteringBootstrap(ctx, collector.workerID, resolve); err != nil {
		return fmt.Errorf("metering bootstrap after %s: %w (sampling: %v)", time.Since(started).Round(time.Millisecond), err, ctx.Err())
	}
	stage = "metering plan"
	plan, err := collector.control.WhiteListMeteringPlan(ctx)
	if err != nil {
		return fmt.Errorf("metering plan after %s: %w (sampling: %v)", time.Since(started).Round(time.Millisecond), err, ctx.Err())
	}
	var candidates []controlplane.WhiteListMeteringAdmissionCandidate
	unchangedByteRoutes := false
	if collector.byteBudgetBytes > 0 {
		stage = "candidate preparation"
		candidates, err = collector.control.WhiteListMeteringAdmissionCandidates(ctx)
		if err != nil {
			return err
		}
		// Compare the full candidate set before funded routes are filtered out.
		// A first admission or changed membership still needs reconciliation.
		unchangedByteRoutes = runtimeWhiteListCandidatesMatchPlan(candidates, plan)
		if refill, ok := collector.control.(runtimeWhiteListByteBudgetRefillControlPlane); ok {
			candidates, err = refill.WhiteListByteBudgetRefillCandidates(ctx, plan, candidates, collector.byteBudgetBytes)
			if err != nil {
				return err
			}
		}
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
	// Plan and candidate discovery grant no runtime authority. Start the fresh
	// sampling window at the first counter read; all admission rechecks follow it.
	ctx, cancelSampling := context.WithTimeout(reconcileContext, processingBudget)
	defer cancelSampling()
	snapshots := make(map[string]sidecaragentclient.UsageSnapshot, len(plan.Origins))
	snapshotReceivedAt := make(map[string]time.Time, len(plan.Origins))
	for _, origin := range plan.Origins {
		stage = "origin usage lookup"
		sender, ok := collector.senders[origin.Origin.NodeID]
		if !ok || sender == nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		lookup, ok := sender.(runtimeWhiteListUsageLookup)
		if !ok {
			return errRuntimeWhiteListMeteringUnavailable
		}
		snapshot, lookupErr := lookup.LookupUsage(ctx, origin.Desired.Action.ActionKey)
		receivedAt := time.Now()
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
		snapshotReceivedAt[origin.Origin.OriginID] = receivedAt
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
		stage = "origin observation persistence"
		if err := collector.control.RecordWhiteListOriginObservation(ctx, controlplane.WhiteListOriginObservation{
			Receipt: origin.Receipt, SampledAt: snapshot.SampledAt,
			AvailableUsers: available, UnavailableUsers: snapshot.UnavailableUsers,
		}); err != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		for _, user := range snapshot.Users {
			stage = "actual usage settlement"
			index := sort.SearchStrings(origin.PendingFirstCumulativeUsers, user.Email)
			firstCumulative := index < len(origin.PendingFirstCumulativeUsers) && origin.PendingFirstCumulativeUsers[index] == user.Email
			if err := collector.applyUser(ctx, origin, routes[user.Email], snapshot.SampledAt, user, firstCumulative); err != nil {
				return errRuntimeWhiteListMeteringUnavailable
			}
		}
	}
	stage = "account admission"
	if err := collector.authorizeAdmissions(ctx, candidates); err != nil {
		return err
	}
	if !leaseEnabled {
		return nil
	}
	stage = "use lease authorization"
	authorization, err := leaseControl.WhiteListUseLeaseAuthorizations(ctx, plan, resolve)
	if err != nil {
		return fmt.Errorf("lease authorization: %w (context: %v)", err, ctx.Err())
	}
	authorizedRoutes := make(map[string]struct{}, len(authorization.Emails))
	for _, email := range authorization.Emails {
		if _, exists := routes[email]; !exists {
			unchangedByteRoutes = false
		}
		if _, duplicate := authorizedRoutes[email]; duplicate {
			unchangedByteRoutes = false
		}
		authorizedRoutes[email] = struct{}{}
	}
	unchangedByteRoutes = unchangedByteRoutes && len(authorizedRoutes) == len(routes)
	// One common conservative budget is anchored to each agent's own earlier
	// read start. Backend wall time is never compared with remote BOOTTIME.
	// Prepare every request before delivering any of them, then deliver once per
	// distinct node in parallel. Sequential cross-Origin delivery can consume a
	// later agent's five-second nonce before its request reaches that agent.
	type leaseDelivery struct {
		originID string
		sender   runtimeWhiteListLeaseSender
		request  sidecaragentclient.UseLeaseRequest
	}
	deliveries := make([]leaseDelivery, 0, len(plan.Origins))
	for _, origin := range plan.Origins {
		if ctx.Err() != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		// FreshFor is remaining time at authorization. Convert it once to a
		// duration from the received snapshot, which is later than the agent's
		// read start. The unchanged BOOTTIME anchor therefore remains conservative.
		budget := authorization.FreshFor
		if !authorization.FreshnessEvaluatedAt.IsZero() {
			budget += authorization.FreshnessEvaluatedAt.Sub(snapshotReceivedAt[origin.Origin.OriginID])
		}
		if budget > 5*time.Second {
			budget = 5 * time.Second
		}
		request, err := sidecaragentclient.NewUseLeaseRequest(snapshots[origin.Origin.OriginID], budget, authorization.Emails)
		if collector.byteBudgetBytes > 0 {
			request, err = sidecaragentclient.NewByteBudgetUseLeaseRequest(snapshots[origin.Origin.OriginID], budget, authorization.Emails,
				authorization.CumulativeByteCeilings[origin.Origin.OriginID], authorization.ByteBudgetFenceGenerations[origin.Origin.OriginID])
		}
		if err != nil {
			return fmt.Errorf("lease request: %w (context: %v)", err, ctx.Err())
		}
		sender := collector.senders[origin.Origin.NodeID].(runtimeWhiteListLeaseSender)
		deliveries = append(deliveries, leaseDelivery{originID: origin.Origin.OriginID, sender: sender, request: request})
	}
	stage = "use lease delivery"
	deliveryErrors := make([]error, len(deliveries))
	var deliveriesWait sync.WaitGroup
	deliveriesWait.Add(len(deliveries))
	for index := range deliveries {
		index := index
		go func() {
			defer deliveriesWait.Done()
			_, deliveryErrors[index] = deliveries[index].sender.PostUseLease(ctx, deliveries[index].request)
		}()
	}
	deliveriesWait.Wait()
	freshNonceNeeded := false
	for index, deliveryErr := range deliveryErrors {
		if errors.Is(deliveryErr, sidecaragentclient.ErrFreshLeaseNonceNeeded) {
			freshNonceNeeded = true
			continue
		}
		if deliveryErr != nil {
			return fmt.Errorf("lease delivery to %s: %w (context: %v)", deliveries[index].originID, deliveryErr, ctx.Err())
		}
	}
	if freshNonceNeeded {
		if unchangedByteRoutes && authorization.ProvisioningComplete {
			collector.reconcileNeeded = false
		}
		return errRuntimeWhiteListFreshLeaseNonce
	}
	if unchangedByteRoutes && authorization.ProvisioningComplete {
		// Every exact existing route was freshly authorized and installed after
		// settlement. Repeating membership reconciliation would change nothing
		// and delay the next sample; every error path retains the normal defer.
		collector.reconcileNeeded = false
	}
	return nil
}

func runtimeWhiteListCandidatesMatchPlan(candidates []controlplane.WhiteListMeteringAdmissionCandidate, plan controlplane.WhiteListMeteringPlan) bool {
	if len(plan.Origins) == 0 || len(plan.Routes) == 0 || len(candidates) != len(plan.Routes) {
		return false
	}
	remaining := make(map[controlplane.WhiteListMeteringAdmissionCandidate]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.EntitlementID == "" || candidate.ExitID == "" {
			return false
		}
		if _, duplicate := remaining[candidate]; duplicate {
			return false
		}
		remaining[candidate] = struct{}{}
	}
	for _, route := range plan.Routes {
		candidate := controlplane.WhiteListMeteringAdmissionCandidate{
			EntitlementID: route.Entitlement.EntitlementID(), ExitID: route.ExitID,
		}
		if _, exists := remaining[candidate]; !exists {
			return false
		}
		delete(remaining, candidate)
	}
	return len(remaining) == 0
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
				if final.Control.Schema == 3 {
					budgetControl, ok := collector.control.(runtimeWhiteListByteBudgetControlPlane)
					if !ok || budgetControl.CompleteWhiteListByteBudgetFinal(ctx, authorization) != nil {
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
				if _, err := sender.PostUseLease(ctx, *page.PendingUseLease); errors.Is(err, sidecaragentclient.ErrFreshLeaseNonceNeeded) {
					return errRuntimeWhiteListFreshLeaseNonce
				} else if err != nil {
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
func (collector *runtimeWhiteListMeteringCollector) authorizeAdmissions(ctx context.Context, prepared ...[]controlplane.WhiteListMeteringAdmissionCandidate) error {
	if collector.byteBudgetBytes > 0 {
		budgetControl, ok := collector.control.(runtimeWhiteListByteBudgetControlPlane)
		if !ok {
			return errRuntimeWhiteListMeteringUnavailable
		}
		var candidates []controlplane.WhiteListMeteringAdmissionCandidate
		if len(prepared) == 1 {
			candidates = prepared[0]
		} else {
			var err error
			candidates, err = collector.control.WhiteListMeteringAdmissionCandidates(ctx)
			if err != nil {
				return fmt.Errorf("candidate discovery: %w", err)
			}
		}
		var admissionErr error
		admitted := 0
		for _, candidate := range candidates {
			if ctx.Err() != nil {
				return errRuntimeWhiteListMeteringUnavailable
			}
			// An exhausted account cannot stop other accounts. Lease authority
			// independently requires a committed positive allocation below.
			if err := budgetControl.AuthorizeWhiteListByteBudgetAdmission(ctx, candidate.EntitlementID, candidate.ExitID, collector.byteBudgetBytes); err != nil {
				admissionErr = err
			} else {
				admitted++
			}
		}
		if admitted == 0 && admissionErr != nil {
			return fmt.Errorf("no byte budget admission: %w (context: %v)", admissionErr, ctx.Err())
		}
		return nil
	}
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
