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
	errRuntimeWhiteListDebitPending        = errors.New("white-list durable commercial debit is pending")
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

type runtimeWhiteListCachedLeaseAuthority struct {
	planFingerprint [sha256.Size]byte
	authorization   controlplane.WhiteListUseLeaseAuthorization
}

type runtimeWhiteListLeaseDelivery struct {
	originID string
	sender   runtimeWhiteListLeaseSender
	request  sidecaragentclient.UseLeaseRequest
}

type runtimeWhiteListMeteringCollector struct {
	control               runtimeWhiteListMeteringControlPlane
	store                 runtimeWhiteListMeteringStore
	workerID              string
	senders               map[string]controlplane.ExternalActionSender
	startupRecovered      bool
	reconcileNeeded       bool
	reserves              runtimeWhiteListReserveProvider
	byteBudgetBytes       int64
	cachedLeaseAuthority  *runtimeWhiteListCachedLeaseAuthority
	leaseAuthorityChanged bool
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
	cachedLeaseAuthority := collector.cachedLeaseAuthority
	collector.cachedLeaseAuthority = nil
	protectedByEarlyLease := false
	passBudget, processingBudget := runtimeWhiteListMeteringPassBudget, runtimeWhiteListMeteringInterval
	if collector.byteBudgetBytes > 0 {
		// Prepaid byte ceilings bound forwarding independently of processing time.
		// Keep the original five-second observation and BOOTTIME lease deadlines;
		// this only lets durable recovery/accounting finish before cancellation.
		passBudget, processingBudget = 15*time.Second, 5*time.Second
	}
	// Cooperative operation bounds, not proof of the live sampling/revoke SLO.
	// Recovery must keep time to reconcile even when sampling exhausts its budget.
	reconcileContext, cancelReconcile := context.WithDeadline(ctx, started.Add(passBudget))
	defer cancelReconcile()
	ctx = reconcileContext
	collector.reconcileNeeded = true
	defer func() {
		preserveExactLease := runErr != nil && collector.byteBudgetBytes > 0 &&
			protectedByEarlyLease && errors.Is(runErr, errRuntimeWhiteListDebitPending)
		if runErr != nil {
			runErr = fmt.Errorf("%s after %s: %w", stage, time.Since(started).Round(time.Millisecond), runErr)
		}
		if preserveExactLease || !collector.reconcileNeeded {
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
		collector.leaseAuthorityChanged = false
		if err := collector.drainFinalReceipts(ctx, leaseControl); err != nil {
			cachedLeaseAuthority = nil
			return fmt.Errorf("final receipt drain: %w", err)
		}
		if collector.leaseAuthorityChanged {
			cachedLeaseAuthority = nil
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
	planFingerprint, planFingerprintOK := runtimeWhiteListLeasePlanFingerprint(plan)
	if cachedLeaseAuthority == nil || !planFingerprintOK || cachedLeaseAuthority.planFingerprint != planFingerprint {
		cachedLeaseAuthority = nil
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
	accountedAvailable := make(map[string]map[string]struct{}, len(plan.Origins))
	freshAvailable := make(map[string]map[string]struct{}, len(plan.Origins))
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
		accounted := make(map[string]struct{}, len(snapshot.Users))
		for _, user := range snapshot.Users {
			if _, ok := routes[user.Email]; !ok {
				return errRuntimeWhiteListMeteringUnavailable
			}
			if _, duplicate := seen[user.Email]; duplicate {
				return errRuntimeWhiteListMeteringUnavailable
			}
			seen[user.Email] = struct{}{}
			accounted[user.Email] = struct{}{}
		}
		if len(seen) != len(routes) {
			return errRuntimeWhiteListMeteringUnavailable
		}
		accountedAvailable[origin.Origin.OriginID] = accounted
	}

	earlyDeliveries, earlyLeaseReady := collector.prepareCachedByteLeaseDeliveries(
		plan, snapshots, snapshotReceivedAt, cachedLeaseAuthority,
	)
	earlyDeliveryErrors := make([]error, len(earlyDeliveries))
	var earlyDeliveriesWait sync.WaitGroup
	if earlyLeaseReady {
		earlyDeliveriesWait.Add(len(earlyDeliveries))
		for index := range earlyDeliveries {
			index := index
			go func() {
				defer earlyDeliveriesWait.Done()
				_, earlyDeliveryErrors[index] = earlyDeliveries[index].sender.PostUseLease(ctx, earlyDeliveries[index].request)
			}()
		}
	}

	var usageErr error
	for _, origin := range plan.Origins {
		snapshot := snapshots[origin.Origin.OriginID]
		available := make([]string, 0, len(snapshot.Users))
		for _, user := range snapshot.Users {
			available = append(available, user.Email)
		}
		stage = "origin observation persistence"
		if err := collector.control.RecordWhiteListOriginObservation(ctx, controlplane.WhiteListOriginObservation{
			Receipt: origin.Receipt, SampledAt: snapshot.SampledAt,
			AvailableUsers: available, UnavailableUsers: snapshot.UnavailableUsers,
		}); err != nil {
			usageErr = errRuntimeWhiteListMeteringUnavailable
			break
		}
	}
	if usageErr != nil {
		return usageErr
	}
	if collector.byteBudgetBytes > 0 {
		// A newly applied desired generation has a new Xray boot ID. Reserve its
		// prepaid byte ceiling after every Origin observation and before accepting
		// that boot's first cumulative counter.
		stage = "account admission"
		if err := collector.authorizeAdmissions(ctx, candidates); err != nil {
			return err
		}
	}
	for _, origin := range plan.Origins {
		snapshot := snapshots[origin.Origin.OriginID]
		for _, user := range snapshot.Users {
			stage = "actual usage settlement"
			index := sort.SearchStrings(origin.PendingFirstCumulativeUsers, user.Email)
			firstCumulative := index < len(origin.PendingFirstCumulativeUsers) && origin.PendingFirstCumulativeUsers[index] == user.Email
			if err := collector.applyUser(ctx, origin, routes[user.Email], snapshot.SampledAt, user, firstCumulative); err != nil {
				if errors.Is(err, errRuntimeWhiteListDebitPending) {
					usageErr = err
				} else {
					usageErr = errRuntimeWhiteListMeteringUnavailable
				}
				break
			}
		}
		if usageErr != nil {
			break
		}
	}

	if earlyLeaseReady {
		earlyDeliveriesWait.Wait()
		stage = "early use lease delivery"
		freshNonceNeeded := false
		for index, deliveryErr := range earlyDeliveryErrors {
			if errors.Is(deliveryErr, sidecaragentclient.ErrFreshLeaseNonceNeeded) {
				freshNonceNeeded = true
				continue
			}
			if deliveryErr != nil {
				return fmt.Errorf("early lease delivery to %s: %w (context: %v)", earlyDeliveries[index].originID, deliveryErr, ctx.Err())
			}
		}
		if freshNonceNeeded {
			return errRuntimeWhiteListFreshLeaseNonce
		}
		protectedByEarlyLease = len(earlyDeliveries) == len(plan.Origins)
	}
	if usageErr != nil {
		return usageErr
	}
	if collector.byteBudgetBytes == 0 {
		stage = "account admission"
		if err := collector.authorizeAdmissions(ctx, candidates); err != nil {
			return err
		}
	}
	if !leaseEnabled {
		return nil
	}
	// Byte accounting and admission writes can consume most of the first
	// five-second sampling phase. Start the separately bounded lease phase and
	// authorize the settled observation before fetching a new agent nonce.
	if collector.byteBudgetBytes > 0 {
		cancelSampling()
		leaseContext, cancelLease := context.WithTimeout(reconcileContext, processingBudget)
		defer cancelLease()
		ctx = leaseContext
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
	if collector.byteBudgetBytes > 0 {
		stage = "lease challenge refresh"
		for _, origin := range plan.Origins {
			sender := collector.senders[origin.Origin.NodeID]
			lookup := sender.(runtimeWhiteListUsageLookup)
			snapshot, lookupErr := lookup.LookupUsage(ctx, origin.Desired.Action.ActionKey)
			receivedAt := time.Now()
			if lookupErr != nil || !runtimeWhiteListUsageReceiptMatches(origin.Receipt, snapshot.Receipt) ||
				snapshot.LeaseChallenge == nil || snapshot.PendingUseLease != nil ||
				len(snapshot.FinalReceipts) > 0 || snapshot.HasMoreFinalReceipts ||
				len(snapshot.Users)+len(snapshot.UnavailableUsers) != len(routes) {
				return errRuntimeWhiteListMeteringUnavailable
			}
			seen := make(map[string]struct{}, len(routes))
			available := make(map[string]struct{}, len(snapshot.Users))
			for _, email := range snapshot.UnavailableUsers {
				if _, ok := routes[email]; !ok {
					return errRuntimeWhiteListMeteringUnavailable
				}
				if _, duplicate := seen[email]; duplicate {
					return errRuntimeWhiteListMeteringUnavailable
				}
				seen[email] = struct{}{}
			}
			for _, user := range snapshot.Users {
				if _, ok := routes[user.Email]; !ok {
					return errRuntimeWhiteListMeteringUnavailable
				}
				if _, duplicate := seen[user.Email]; duplicate {
					return errRuntimeWhiteListMeteringUnavailable
				}
				seen[user.Email] = struct{}{}
				available[user.Email] = struct{}{}
			}
			if len(seen) != len(routes) {
				return errRuntimeWhiteListMeteringUnavailable
			}
			// This second read refreshes only the short-lived agent nonce. Its
			// counters are settled by the next pass before they can advance the
			// durable accounted-through observation.
			freshAvailable[origin.Origin.OriginID] = available
			snapshots[origin.Origin.OriginID] = snapshot
			snapshotReceivedAt[origin.Origin.OriginID] = receivedAt
		}
	}
	// One common conservative budget is anchored to each agent's own earlier
	// read start. Backend wall time is never compared with remote BOOTTIME.
	// Prepare every request before delivering any of them, then deliver once per
	// distinct node in parallel. Sequential cross-Origin delivery can consume a
	// later agent's five-second nonce before its request reaches that agent.
	deliveries := make([]runtimeWhiteListLeaseDelivery, 0, len(plan.Origins))
	for _, origin := range plan.Origins {
		if ctx.Err() != nil {
			return errRuntimeWhiteListMeteringUnavailable
		}
		if collector.byteBudgetBytes > 0 {
			for email := range authorizedRoutes {
				_, accounted := accountedAvailable[origin.Origin.OriginID][email]
				_, fresh := freshAvailable[origin.Origin.OriginID][email]
				if accounted != fresh {
					return errRuntimeWhiteListMeteringUnavailable
				}
			}
		}
		// FreshFor is remaining time at authorization. Convert it once to a
		// duration from the received snapshot, which is later than the agent's
		// read start. The unchanged BOOTTIME anchor therefore remains conservative.
		budget := authorization.FreshFor
		if !authorization.FreshnessEvaluatedAt.IsZero() {
			budget += authorization.FreshnessEvaluatedAt.Sub(snapshotReceivedAt[origin.Origin.OriginID])
		}
		if collector.byteBudgetBytes > 0 && len(authorization.Emails) > 0 {
			budget = 5 * time.Second
			hardRemaining := authorization.AuthorityExpiresAt.Sub(snapshotReceivedAt[origin.Origin.OriginID])
			if hardRemaining < budget {
				budget = hardRemaining
			}
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
		deliveries = append(deliveries, runtimeWhiteListLeaseDelivery{originID: origin.Origin.OriginID, sender: sender, request: request})
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
	if collector.byteBudgetBytes > 0 && planFingerprintOK && len(authorization.Emails) > 0 {
		collector.cachedLeaseAuthority = &runtimeWhiteListCachedLeaseAuthority{
			planFingerprint: planFingerprint,
			authorization:   runtimeWhiteListCloneUseLeaseAuthorization(authorization),
		}
	}
	if unchangedByteRoutes && authorization.ProvisioningComplete {
		// Every exact existing route was freshly authorized and installed after
		// settlement. Repeating membership reconciliation would change nothing
		// and delay the next sample; every error path retains the normal defer.
		collector.reconcileNeeded = false
	}
	return nil
}

func runtimeWhiteListLeasePlanFingerprint(plan controlplane.WhiteListMeteringPlan) ([sha256.Size]byte, bool) {
	var empty [sha256.Size]byte
	if len(plan.Origins) == 0 || len(plan.Routes) == 0 {
		return empty, false
	}
	origins := append([]controlplane.WhiteListMeteringOrigin(nil), plan.Origins...)
	sort.Slice(origins, func(i, j int) bool { return origins[i].Origin.OriginID < origins[j].Origin.OriginID })
	routes := append([]controlplane.WhiteListMeteringRoute(nil), plan.Routes...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].ManagedEmail < routes[j].ManagedEmail })
	digest := sha256.New()
	writeField := func(value string) {
		_, _ = digest.Write([]byte(strconv.Itoa(len(value))))
		_, _ = digest.Write([]byte{':'})
		_, _ = digest.Write([]byte(value))
	}
	writeField("maestro-white-list-lease-plan-v1")
	for index, origin := range origins {
		if origin.Origin.OriginID == "" || origin.Origin.NodeID == "" || origin.Desired.Action.ActionKey == "" ||
			origin.Receipt.XrayProcessBootID == "" || (index > 0 && origins[index-1].Origin.OriginID == origin.Origin.OriginID) {
			return empty, false
		}
		for _, value := range []string{
			"origin", origin.Origin.OriginID, origin.Origin.NodeID, origin.Origin.ReleaseID,
			origin.Origin.ProfileID, origin.Origin.PresetID, origin.Origin.ConfigDigest,
			strconv.FormatBool(origin.Origin.Active), origin.Desired.OriginID, origin.Desired.NodeID,
			origin.Desired.ReleaseID, origin.Desired.ProfileID, origin.Desired.PresetID,
			origin.Desired.ExitID, strconv.FormatInt(origin.Desired.Generation, 10),
			origin.Desired.ConfigDigest, origin.Desired.ManagedUserSetDigest, origin.Desired.DesiredSHA256,
			origin.Desired.Action.Type, origin.Desired.Action.ResourceID, origin.Desired.Action.ActionKey,
			origin.Desired.Action.ReplacesActionKey, strconv.FormatInt(origin.Desired.Action.LeaseFence, 10),
			origin.Receipt.ActionKey, origin.Receipt.OriginID, origin.Receipt.ReleaseID,
			origin.Receipt.XrayProcessBootID, origin.Receipt.ConfigDigest,
			strconv.FormatInt(origin.Receipt.DesiredGeneration, 10), origin.Receipt.ManagedUserSetDigest,
			strconv.FormatInt(origin.Receipt.AppliedAt.UnixNano(), 10),
			strconv.FormatInt(origin.Receipt.ExpiresAt.UnixNano(), 10),
		} {
			writeField(value)
		}
	}
	for index, route := range routes {
		if route.ManagedEmail == "" || route.ExitID == "" || route.Entitlement.EntitlementID() == "" ||
			(index > 0 && routes[index-1].ManagedEmail == route.ManagedEmail) {
			return empty, false
		}
		for _, value := range []string{
			"route", route.ManagedEmail, route.ExitID, route.Entitlement.EntitlementID(),
			string(route.Entitlement.State()), route.Entitlement.TransportProfileID(),
			route.Entitlement.CompatibilityPresetID(), route.Entitlement.TransportReleaseID(),
			route.Policy.BillingPeriodID, strconv.FormatInt(route.Policy.PeriodStartsAtUnix, 10),
			strconv.FormatInt(route.Policy.PeriodEndsAtUnix, 10), route.Policy.Unit, route.Policy.Basis,
			strconv.FormatUint(route.Policy.IncludedBytes, 10), strconv.FormatUint(route.Policy.SoftLimitBytes, 10),
			strconv.FormatUint(route.Policy.HardLimitBytes, 10), strconv.FormatUint(route.Policy.GraceBytes, 10),
			route.Policy.PriceMode, route.Policy.PriceSource, route.Policy.Currency,
			strconv.FormatUint(route.Policy.MinorUnitsPerUnit, 10),
		} {
			writeField(value)
		}
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], digest.Sum(nil))
	return fingerprint, true
}

func runtimeWhiteListCloneUseLeaseAuthorization(source controlplane.WhiteListUseLeaseAuthorization) controlplane.WhiteListUseLeaseAuthorization {
	clone := source
	clone.Emails = append([]string(nil), source.Emails...)
	clone.CumulativeByteCeilings = make(map[string]map[string]int64, len(source.CumulativeByteCeilings))
	for originID, sourceCeilings := range source.CumulativeByteCeilings {
		ceilings := make(map[string]int64, len(sourceCeilings))
		for email, ceiling := range sourceCeilings {
			ceilings[email] = ceiling
		}
		clone.CumulativeByteCeilings[originID] = ceilings
	}
	clone.ByteBudgetFenceGenerations = make(map[string]map[string]uint64, len(source.ByteBudgetFenceGenerations))
	for originID, sourceFences := range source.ByteBudgetFenceGenerations {
		fences := make(map[string]uint64, len(sourceFences))
		for email, generation := range sourceFences {
			fences[email] = generation
		}
		clone.ByteBudgetFenceGenerations[originID] = fences
	}
	return clone
}

func (collector *runtimeWhiteListMeteringCollector) prepareCachedByteLeaseDeliveries(
	plan controlplane.WhiteListMeteringPlan,
	snapshots map[string]sidecaragentclient.UsageSnapshot,
	snapshotReceivedAt map[string]time.Time,
	cached *runtimeWhiteListCachedLeaseAuthority,
) ([]runtimeWhiteListLeaseDelivery, bool) {
	fingerprint, fingerprintOK := runtimeWhiteListLeasePlanFingerprint(plan)
	if collector == nil || cached == nil || !fingerprintOK || fingerprint != cached.planFingerprint ||
		len(plan.Origins) == 0 || len(plan.Routes) == 0 || len(snapshots) != len(plan.Origins) ||
		len(snapshotReceivedAt) != len(plan.Origins) {
		return nil, false
	}
	authorization := cached.authorization
	if len(authorization.Emails) == 0 || !sort.StringsAreSorted(authorization.Emails) ||
		authorization.AuthorityExpiresAt.IsZero() || len(authorization.CumulativeByteCeilings) != len(plan.Origins) ||
		len(authorization.ByteBudgetFenceGenerations) != len(plan.Origins) {
		return nil, false
	}
	routes := make(map[string]struct{}, len(plan.Routes))
	for _, route := range plan.Routes {
		if route.ManagedEmail == "" {
			return nil, false
		}
		routes[route.ManagedEmail] = struct{}{}
	}
	authorized := make(map[string]struct{}, len(authorization.Emails))
	for index, email := range authorization.Emails {
		if _, current := routes[email]; !current || (index > 0 && authorization.Emails[index-1] == email) {
			return nil, false
		}
		authorized[email] = struct{}{}
	}
	deliveries := make([]runtimeWhiteListLeaseDelivery, 0, len(plan.Origins))
	for _, origin := range plan.Origins {
		snapshot, snapshotOK := snapshots[origin.Origin.OriginID]
		receivedAt, receivedOK := snapshotReceivedAt[origin.Origin.OriginID]
		originCeilings, ceilingsOK := authorization.CumulativeByteCeilings[origin.Origin.OriginID]
		originFences, fencesOK := authorization.ByteBudgetFenceGenerations[origin.Origin.OriginID]
		if !snapshotOK || !receivedOK || receivedAt.IsZero() || !ceilingsOK || !fencesOK ||
			len(snapshot.Users) != len(authorization.Emails) || len(originCeilings) != len(authorization.Emails) ||
			len(originFences) != len(authorization.Emails) {
			return nil, false
		}
		emails := make([]string, 0, len(snapshot.Users))
		seen := make(map[string]struct{}, len(snapshot.Users))
		ceilings := make(map[string]int64, len(snapshot.Users))
		fences := make(map[string]uint64, len(snapshot.Users))
		for _, user := range snapshot.Users {
			if _, allowed := authorized[user.Email]; !allowed {
				return nil, false
			}
			if _, duplicate := seen[user.Email]; duplicate {
				return nil, false
			}
			ceiling, ceilingOK := originCeilings[user.Email]
			fence, fenceOK := originFences[user.Email]
			if !ceilingOK || !fenceOK || ceiling <= 0 || user.UplinkBytes > uint64(ceiling) ||
				user.DownlinkBytes > uint64(ceiling)-user.UplinkBytes {
				return nil, false
			}
			seen[user.Email] = struct{}{}
			emails = append(emails, user.Email)
			ceilings[user.Email] = ceiling
			fences[user.Email] = fence
		}
		if len(seen) != len(authorization.Emails) {
			return nil, false
		}
		sort.Strings(emails)
		budget := 5 * time.Second
		if hardRemaining := authorization.AuthorityExpiresAt.Sub(receivedAt); hardRemaining < budget {
			budget = hardRemaining
		}
		if budget <= 0 {
			return nil, false
		}
		request, err := sidecaragentclient.NewByteBudgetUseLeaseRequest(snapshot, budget, emails, ceilings, fences)
		if err != nil {
			return nil, false
		}
		sender, ok := collector.senders[origin.Origin.NodeID].(runtimeWhiteListLeaseSender)
		if !ok {
			return nil, false
		}
		deliveries = append(deliveries, runtimeWhiteListLeaseDelivery{
			originID: origin.Origin.OriginID, sender: sender, request: request,
		})
	}
	return deliveries, len(deliveries) == len(plan.Origins)
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
			if len(page.FinalReceipts) > 0 || page.PendingUseLease != nil {
				collector.leaseAuthorityChanged = true
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
		if err := collector.store.DrainCommercialDebits(ctx, route.Entitlement.EntitlementID(), collector.control); err != nil {
			return fmt.Errorf("%w: %v", errRuntimeWhiteListDebitPending, err)
		}
		return nil
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
		return fmt.Errorf("%w: %v", errRuntimeWhiteListDebitPending, err)
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
