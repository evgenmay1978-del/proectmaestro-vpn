package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/runtimefence"
)

type leaseTestHandler struct {
	*fakeHandler
	t           *testing.T
	store       *FileStore
	clock       *int64
	counters    map[string][2]uint64
	controls    []runtimefence.Control
	reads       []string
	unknownOnce bool
	failFence   bool
	onControl   func(runtimefence.Control)
	onStats     func()
}

func (handler *leaseTestHandler) ManagedUserCounters(_ context.Context, emails []string) (map[string][2]uint64, error) {
	handler.reads = append(handler.reads, "stats")
	if handler.onStats != nil {
		handler.onStats()
	}
	result := map[string][2]uint64{}
	for _, email := range emails {
		if value, ok := handler.counters[email]; ok {
			result[email] = value
		}
	}
	return result, nil
}

func (handler *leaseTestHandler) ApplyManagedControl(ctx context.Context, control runtimefence.Control) (runtimefence.Receipt, error) {
	if _, ok := ctx.Deadline(); !ok {
		handler.t.Fatal("runtime RPC had no deadline")
	}
	state, err := handler.store.loadLeaseState()
	if err != nil || state.Pending == nil || state.Pending.Next >= len(state.Pending.Steps) || state.Pending.Steps[state.Pending.Next].Control == nil ||
		*state.Pending.Steps[state.Pending.Next].Control != control || state.Users[leaseUserKey(control.BootID, control.Email)].Generation != control.Generation {
		handler.t.Fatal("RPC preceded durable full command and generation reservation")
	}
	if state.Pending.Request != nil && state.Challenge != nil {
		handler.t.Fatal("nonce was not atomically consumed with pending command")
	}
	handler.controls = append(handler.controls, control)
	if handler.onControl != nil {
		handler.onControl(control)
	}
	if handler.unknownOnce {
		handler.unknownOnce = false
		return runtimefence.Receipt{}, errors.New("synthetic uncertain transport")
	}
	if handler.failFence && control.Operation == "fence" {
		return runtimefence.Receipt{}, errors.New("synthetic drain incomplete")
	}
	receipt := runtimefence.Receipt{Schema: 2, Email: control.Email, BootID: control.BootID, ConfigDigest: control.ConfigDigest, Generation: control.Generation,
		ClockDomain: control.ClockDomain, ObservedAt: "2026-09-06T00:00:00Z"}
	if control.Operation == "fence" {
		if counters, ok := handler.counters[control.Email]; ok {
			up, down := int64(counters[0]), int64(counters[1])
			receipt.State = "fenced"
			receipt.Uplink = &up
			receipt.Downlink = &down
		} else {
			receipt.State = "fenced_unused"
		}
		return receipt, nil
	}
	remaining := uint32(0)
	if control.DeadlineBoottimeNS > *handler.clock {
		remaining = uint32((control.DeadlineBoottimeNS - *handler.clock) / int64(time.Millisecond))
	}
	receipt.State = "granted"
	receipt.DeadlineBoottimeNS = control.DeadlineBoottimeNS
	receipt.LeaseRemainingMS = &remaining
	if remaining == 0 {
		return receipt, errors.New("synthetic valid receipt expired after RPC")
	}
	return receipt, nil
}

func (handler *leaseTestHandler) RemoveUser(ctx context.Context, tag, email string) error {
	state, err := handler.store.loadLeaseState()
	if err != nil {
		handler.t.Fatal(err)
	}
	found := false
	for _, final := range state.FinalReceipts {
		if final.Control.Email == email {
			found = true
		}
	}
	if !found {
		handler.t.Fatal("RemoveUser preceded durable real drain receipt")
	}
	return handler.fakeHandler.RemoveUser(ctx, tag, email)
}

type leaseFixture struct {
	r       *Reconciler
	store   *FileStore
	handler *leaseTestHandler
	desired Desired
	wall    time.Time
	clock   int64
	boot    string
}

func newLeaseFixture(t *testing.T) *leaseFixture {
	t.Helper()
	f := &leaseFixture{wall: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), clock: int64(10 * time.Second), boot: "physical-commercial-boot"}
	var err error
	f.store, err = NewFileStore(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = &leaseTestHandler{fakeHandler: newFakeHandler("canary:fixed", "ordinary:fixed"), t: t, store: f.store, clock: &f.clock, counters: map[string][2]uint64{}}
	f.r = f.reopen(t)
	f.desired = testDesired(t, 1, "release-12", strings.Repeat("a", 64), "wl:one:exit-s1")
	if _, err := f.r.Apply(context.Background(), f.desired); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *leaseFixture) reopen(t *testing.T) *Reconciler {
	t.Helper()
	r, err := NewReconciler(ReconcilerConfig{Handler: f.handler, Store: f.store, InboundTag: DefaultInboundTag, ReleaseID: "release-12", ConfigDigest: strings.Repeat("a", 64),
		ProcessBootID: func() (string, error) { return f.boot, nil }, Preflight: &usagePreflight{}, Now: func() time.Time { return f.wall }, ManagedLeaseEnabled: true,
		LeaseClock: func() (string, int64, error) {
			f.handler.reads = append(f.handler.reads, "clock")
			return strings.Repeat("c", 64), f.clock, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *leaseFixture) ackAll(t *testing.T) {
	t.Helper()
	for {
		page, err := f.r.LeaseReceipts(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(page.FinalReceipts) == 0 {
			return
		}
		ack := LeaseReceiptAck{Schema: 2}
		for _, final := range page.FinalReceipts {
			ack.Receipts = append(ack.Receipts, LeaseReceiptAckItem{final.ReceiptID, final.ProofSHA256})
		}
		if err := f.r.AckLeaseReceipts(context.Background(), ack); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *leaseFixture) request(t *testing.T, emails ...string) UseLeaseRequest {
	t.Helper()
	usage, err := f.r.Usage(context.Background(), f.desired.ActionKey())
	if err != nil || usage.LeaseChallenge == nil {
		t.Fatal("fresh usage challenge unavailable")
	}
	c := usage.LeaseChallenge
	return UseLeaseRequest{Schema: 2, ActionKey: usage.Receipt.ActionKey, XrayProcessBootID: usage.Receipt.XrayProcessBootID, ConfigDigest: usage.Receipt.ConfigDigest,
		ManagedUserSetDigest: usage.Receipt.ManagedUserSetDigest, Nonce: c.Nonce, ClockDomain: c.ClockDomain, ReadStartedBoottimeNS: c.ReadStartedBoottimeNS, DeadlineBoottimeNS: c.MaxDeadlineBoottimeNS, Emails: append([]string{}, emails...)}
}

func TestCommercialBootstrapRequiresTrueUnusedProofAckAndPreStatsClock(t *testing.T) {
	f := newLeaseFixture(t)
	if len(f.handler.controls) != 1 || f.handler.controls[0].Operation != "fence" {
		t.Fatal("desired bootstrap granted runtime permission")
	}
	f.handler.reads = nil
	usage, err := f.r.Usage(context.Background(), f.desired.ActionKey())
	if err != nil || !reflect.DeepEqual(usage.UnavailableUsers, []string{"wl:one:exit-s1"}) || len(usage.Users) != 0 || usage.LeaseChallenge == nil ||
		len(f.handler.reads) < 2 || f.handler.reads[0] != "clock" || f.handler.reads[1] != "stats" {
		t.Fatal("bootstrap fabricated zero or sampled clock after counters")
	}
	page, err := f.r.LeaseReceipts(context.Background())
	if err != nil || len(page.FinalReceipts) != 1 || page.FinalReceipts[0].Receipt.State != "fenced_unused" || page.FinalReceipts[0].Receipt.Uplink != nil {
		t.Fatal("unused proof missing")
	}
	request := f.request(t, "wl:one:exit-s1")
	if _, err := f.r.UseLease(context.Background(), request); !errors.Is(err, ErrLeasePending) || len(f.handler.controls) != 1 {
		t.Fatal("unacknowledged tail permitted grant")
	}
	f.ackAll(t)
	result, err := f.r.UseLease(context.Background(), request)
	if err != nil || !result.Complete || len(result.Receipts) != 1 || result.Receipts[0].Control.Operation != "grant" || result.Receipts[0].Control.Generation != 2 {
		t.Fatal("fresh authorization did not use the reserved runtime grant")
	}
}

func TestLeaseUnknownRetryPersistsExactCommandAcrossAgentRestart(t *testing.T) {
	f := newLeaseFixture(t)
	f.ackAll(t)
	request := f.request(t, "wl:one:exit-s1")
	f.handler.unknownOnce = true
	if _, err := f.r.UseLease(context.Background(), request); !errors.Is(err, ErrLeasePending) {
		t.Fatal("uncertain RPC was treated as complete")
	}
	first := f.handler.controls[len(f.handler.controls)-1]
	page, err := f.r.LeaseReceipts(context.Background())
	if err != nil || page.PendingUseLease == nil || !reflect.DeepEqual(*page.PendingUseLease, request) {
		t.Fatal("restart recovery did not expose exact pending request")
	}
	f.r = f.reopen(t)
	changed := request
	changed.DeadlineBoottimeNS--
	if _, err := f.r.UseLease(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("same nonce accepted changed request")
	}
	result, err := f.r.UseLease(context.Background(), request)
	if err != nil || !result.Complete || f.handler.controls[len(f.handler.controls)-1] != first {
		t.Fatal("unknown retry reminted operation or deadline")
	}
	count := len(f.handler.controls)
	if _, err := f.r.UseLease(context.Background(), request); err != nil || len(f.handler.controls) != count {
		t.Fatal("completed retry repeated runtime mutation")
	}
}

func TestExpiredReceiptNeverRearmsFromSameNonce(t *testing.T) {
	f := newLeaseFixture(t)
	f.ackAll(t)
	request := f.request(t, "wl:one:exit-s1")
	f.handler.onControl = func(control runtimefence.Control) {
		if control.Operation == "grant" {
			f.clock = control.DeadlineBoottimeNS
		}
	}
	result, err := f.r.UseLease(context.Background(), request)
	if err == nil || result.Complete || !result.NeedsFreshNonce || len(result.Receipts) != 1 || result.Receipts[0].Receipt.State != "granted" {
		t.Fatal("valid expired receipt was discarded or treated as authority")
	}
	f.handler.onControl = nil
	count := len(f.handler.controls)
	if retry, err := f.r.UseLease(context.Background(), request); err == nil || retry.Complete || len(f.handler.controls) != count {
		t.Fatal("expired retry rearmed the same identity")
	}
	f.handler.counters["wl:one:exit-s1"] = [2]uint64{13, 29}
	fresh := f.request(t, "wl:one:exit-s1")
	result, err = f.r.UseLease(context.Background(), fresh)
	if err == nil || result.Complete || !result.NeedsFreshNonce || f.handler.controls[len(f.handler.controls)-1].Operation != "fence" {
		t.Fatal("rearm nonce granted permission")
	}
	page, _ := f.r.LeaseReceipts(context.Background())
	if len(page.FinalReceipts) != 1 || page.FinalReceipts[0].Receipt.Uplink == nil || *page.FinalReceipts[0].Receipt.Uplink != 13 || *page.FinalReceipts[0].Receipt.Downlink != 29 {
		t.Fatal("real drained counters were lost during replacement")
	}
	f.ackAll(t)
	next := f.request(t, "wl:one:exit-s1")
	if next.Nonce == fresh.Nonce {
		t.Fatal("fresh MemoryUser reused rearm nonce")
	}
	if result, err := f.r.UseLease(context.Background(), next); err != nil || !result.Complete {
		t.Fatal("fresh nonce did not authorize replacement user")
	}
}

func TestPendingGrantIsNeverReplayedByDesiredRefresh(t *testing.T) {
	f := newLeaseFixture(t)
	f.ackAll(t)
	request := f.request(t, "wl:one:exit-s1")
	f.handler.unknownOnce = true
	_, _ = f.r.UseLease(context.Background(), request)
	count := len(f.handler.controls)
	if _, err := f.r.Refresh(context.Background()); !errors.Is(err, ErrLeasePending) || len(f.handler.controls) != count {
		t.Fatal("readiness refresh replayed grant")
	}
	f.clock = request.DeadlineBoottimeNS
	if _, err := f.r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, control := range f.handler.controls[count:] {
		if control.Operation != "fence" {
			t.Fatal("expired pending grant was renewed by refresh")
		}
	}
	page, _ := f.r.LeaseReceipts(context.Background())
	if len(page.FinalReceipts) != 1 {
		t.Fatal("uncertain permission was not durably drained")
	}
}

func TestDesiredRemovalRetainsFinalCountersBeyondReadinessExpiry(t *testing.T) {
	f := newLeaseFixture(t)
	f.ackAll(t)
	request := f.request(t, "wl:one:exit-s1")
	if _, err := f.r.UseLease(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	f.handler.counters["wl:one:exit-s1"] = [2]uint64{400, 900}
	removed := testDesired(t, 2, "release-12", strings.Repeat("a", 64), []string{}...)
	if _, err := f.r.Apply(context.Background(), removed); err != nil {
		t.Fatal(err)
	}
	if _, exists := f.handler.users["wl:one:exit-s1"]; exists {
		t.Fatal("managed user not removed after drain")
	}
	f.wall = f.wall.Add(time.Hour)
	if _, err := f.r.Usage(context.Background(), removed.ActionKey()); err == nil {
		t.Fatal("expired readiness unexpectedly accepted")
	}
	page, err := f.r.LeaseReceipts(context.Background())
	if err != nil || len(page.FinalReceipts) != 1 || page.FinalReceipts[0].ActionKey != f.desired.ActionKey() || *page.FinalReceipts[0].Receipt.Downlink != 900 {
		t.Fatal("removed user's old binding/final counters disappeared")
	}
}

func TestFailedDrainNeverRemovesOrReplacesUser(t *testing.T) {
	f := newLeaseFixture(t)
	f.ackAll(t)
	f.handler.failFence = true
	f.handler.accountOK["wl:one:exit-s1"] = false
	before := append([]string(nil), f.handler.operations...)
	if _, err := f.r.Refresh(context.Background()); err == nil {
		t.Fatal("incomplete drain was accepted")
	}
	if !reflect.DeepEqual(before, f.handler.operations) {
		t.Fatal("remove/add ran before proven drain")
	}
}

func TestCompletedGrantRetryObservesSubsequentDesiredRevocation(t *testing.T) {
	f := newLeaseFixture(t)
	f.ackAll(t)
	request := f.request(t, "wl:one:exit-s1")
	if result, err := f.r.UseLease(context.Background(), request); err != nil || !result.Complete {
		t.Fatal("initial grant failed")
	}
	removed := testDesired(t, 2, "release-12", strings.Repeat("a", 64), []string{}...)
	if _, err := f.r.Apply(context.Background(), removed); err != nil {
		t.Fatal(err)
	}
	if f.clock >= request.DeadlineBoottimeNS {
		t.Fatal("fixture must revoke before lease expiry")
	}
	before := len(f.handler.controls)
	result, err := f.r.UseLease(context.Background(), request)
	if err == nil || result.Complete || !result.NeedsFreshNonce || len(f.handler.controls) != before || len(result.Receipts) != 1 {
		t.Fatal("cached grant survived current desired revocation or replayed RPC")
	}
}

func TestFirstCommercialPreflightFailurePreservesHistoricalDesiredBinding(t *testing.T) {
	f := newLeaseFixture(t)
	f.ackAll(t)
	// Model explicit commercial enable after an upstream deployment: desired
	// exists but the commercial journal has not yet adopted those identities.
	if err := f.store.saveLeaseState(emptyLeaseState()); err != nil {
		t.Fatal(err)
	}
	preflight := &usagePreflight{fakeReadinessPreflight: fakeReadinessPreflight{err: errors.New("synthetic preflight not ready")}}
	f.r.preflight = preflight
	removed := testDesired(t, 2, "release-12", strings.Repeat("a", 64), []string{}...)
	if _, err := f.r.Apply(context.Background(), removed); err == nil {
		t.Fatal("invalid preflight accepted")
	}
	stored, err := f.store.LoadDesired()
	if err != nil || stored.ActionKey() != f.desired.ActionKey() {
		t.Fatal("failed preflight erased untracked historical managed binding")
	}
	preflight.err = nil
	if _, err := f.r.Apply(context.Background(), removed); err != nil {
		t.Fatal(err)
	}
	page, err := f.r.LeaseReceipts(context.Background())
	if err != nil || len(page.FinalReceipts) != 1 || page.FinalReceipts[0].ActionKey != f.desired.ActionKey() {
		t.Fatal("first-enable removal invented or lost historical accounting binding")
	}
}
