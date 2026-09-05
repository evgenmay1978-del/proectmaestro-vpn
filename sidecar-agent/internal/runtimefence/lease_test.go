package runtimefence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Only tests replace the BOOTTIME reader. Access to this value uses gate.mu,
// including timer callbacks, so simulated suspend does not introduce a race.
func freezeLeaseClock(g *gate) *int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.lastClockNS
	g.clockNow = func() (int64, error) { return now, nil }
	return &now
}

func advanceLeaseClock(g *gate, now *int64, duration time.Duration) {
	g.mu.Lock()
	*now += int64(duration)
	g.mu.Unlock()
}

func TestAbsoluteLeaseBoundsRenewalAndNonExtendingExactRetry(t *testing.T) {
	g, user, sm, c := fixture(t)
	clock := freezeLeaseClock(g)
	for _, deadline := range []int64{-1, 0, *clock, *clock + int64(maxLease) + 1, math.MaxInt64} {
		bad := c
		bad.DeadlineBoottimeNS = deadline
		if r, err := g.apply(context.Background(), bad, user, sm); err == nil || r != nil {
			t.Fatalf("invalid absolute deadline accepted: %d", deadline)
		}
	}
	bad := c
	bad.Operation = "renew"
	if _, err := g.apply(context.Background(), bad, user, sm); err == nil {
		t.Fatal("renew created an initial grant")
	}
	bad.Operation = "fence"
	if _, err := g.apply(context.Background(), bad, nil, sm); err == nil {
		t.Fatal("fence accepted a lease deadline")
	}
	first, err := g.apply(context.Background(), c, user, sm)
	if err != nil {
		t.Fatal(err)
	}
	if first.Schema != 2 || first.ClockDomain != g.clockDomain || first.DeadlineBoottimeNS != c.DeadlineBoottimeNS || first.LeaseRemainingMS == nil || *first.LeaseRemainingMS != 5000 {
		t.Fatal("missing or shifted absolute lease receipt")
	}
	g.mu.Lock()
	state := g.users[user.Email]
	originalDeadline, originalTimer := state.leaseDeadlineNS, state.leaseTimer
	g.mu.Unlock()
	advanceLeaseClock(g, clock, 200*time.Millisecond+time.Nanosecond)
	duplicate, err := g.apply(context.Background(), c, user, sm)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.DeadlineBoottimeNS != first.DeadlineBoottimeNS || duplicate.LeaseRemainingMS == nil || *duplicate.LeaseRemainingMS != 4799 {
		t.Fatal("duplicate extended/rounded-up lease")
	}
	bad = c
	bad.DeadlineBoottimeNS--
	if _, err := g.apply(context.Background(), bad, user, sm); err == nil {
		t.Fatal("changed retry deadline accepted")
	}
	g.mu.Lock()
	unchanged := state.leaseDeadlineNS == originalDeadline && state.leaseTimer == originalTimer
	g.mu.Unlock()
	if !unchanged {
		t.Fatal("retry rearmed the lease timer")
	}
	renew := c
	renew.Operation = "renew"
	renew.DeadlineBoottimeNS = deadlineAfter(t, g, maxLease)
	if _, err := g.apply(context.Background(), renew, user, sm); err == nil {
		t.Fatal("renew reused grant generation")
	}
	renew.Generation++
	replacement := &protocol.MemoryUser{Email: user.Email, Account: &vless.MemoryAccount{}}
	if _, err := g.apply(context.Background(), renew, replacement, sm); err == nil {
		t.Fatal("renew changed authenticated identity")
	}
	renewed, err := g.apply(context.Background(), renew, user, sm)
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	renewedDeadline, renewedTimer := state.leaseDeadlineNS, state.leaseTimer
	g.mu.Unlock()
	if renewedDeadline <= originalDeadline || renewedTimer == originalTimer {
		t.Fatal("fresh renewal did not use the new authorized absolute deadline")
	}
	advanceLeaseClock(g, clock, 100*time.Millisecond)
	duplicate, err = g.apply(context.Background(), renew, user, sm)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.DeadlineBoottimeNS != renewed.DeadlineBoottimeNS || *duplicate.LeaseRemainingMS >= *renewed.LeaseRemainingMS {
		t.Fatal("renew retry extended lease")
	}
	if _, err := g.apply(context.Background(), c, user, sm); err == nil {
		t.Fatal("older grant replay accepted after renewal")
	}
	advanceLeaseClock(g, clock, 4750*time.Millisecond)
	g.expireLease(state, c.Generation, originalDeadline)
	g.mu.Lock()
	stillAllowed := state.allowed && state.leaseDeadlineNS == renewedDeadline && state.leaseTimer == renewedTimer
	g.mu.Unlock()
	if !stillAllowed {
		t.Fatal("stale timer callback changed renewed lease")
	}
}

func TestLateDeliveryCannotShiftAuthorityCutoff(t *testing.T) {
	for _, renewing := range []bool{false, true} {
		name := "grant"
		if renewing {
			name = "renew"
		}
		t.Run(name, func(t *testing.T) {
			g, user, sm, c := fixture(t)
			clock := freezeLeaseClock(g)
			if renewing {
				if _, err := g.apply(context.Background(), c, user, sm); err != nil {
					t.Fatal(err)
				}
				c.Operation = "renew"
				c.Generation++
			}
			c.DeadlineBoottimeNS = deadlineAfter(t, g, 500*time.Millisecond)
			advanceLeaseClock(g, clock, time.Second) // The request was delayed in transit.
			if r, err := g.apply(context.Background(), c, user, sm); err == nil || r != nil {
				t.Fatal("late RPC became a fresh runtime lease")
			}
			g.mu.Lock()
			state := g.users[user.Email]
			allowed := state != nil && state.allowed
			g.mu.Unlock()
			if !renewing && allowed {
				t.Fatal("late initial grant opened admission")
			}
			if renewing {
				advanceLeaseClock(g, clock, 5*time.Second)
				if _, _, err := g.start(context.Background(), user, func() {}); err == nil {
					t.Fatal("denied renewal extended original lease")
				}
			}
		})
	}
}

func TestClockDomainBootAndOldWireSchemasFailClosed(t *testing.T) {
	g, user, sm, c := fixture(t)
	for _, change := range []func(*Control){
		func(c *Control) { c.ClockDomain = "another-host-or-time-namespace" },
		func(c *Control) { c.BootID = "another-process-boot" },
		func(c *Control) { c.Schema = 1 },
	} {
		bad := c
		change(&bad)
		if r, err := g.apply(context.Background(), bad, user, sm); err == nil || r != nil {
			t.Fatal("foreign clock/boot/schema accepted")
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Control
	if decode(raw, &decoded) != nil || decoded.ClockDomain != g.clockDomain || decoded.DeadlineBoottimeNS != c.DeadlineBoottimeNS {
		t.Fatal("schema2 absolute request rejected")
	}
	oldSchema := bytes.Replace(raw, []byte("\"schema\":2"), []byte("\"schema\":1"), 1)
	durationOnly := []byte("{\"schema\":2,\"operation\":\"grant\",\"lease_ms\":5000}")
	withDuration := append(append([]byte{}, bytes.TrimSuffix(raw, []byte("}"))...), []byte(",\"lease_ms\":5000}")...)
	for _, wire := range [][]byte{oldSchema, durationOnly, withDuration} {
		if decode(wire, &decoded) == nil {
			t.Fatal("legacy/duration wire decoded")
		}
		// Rejection must precede user lookup, including with no dependencies.
		_, err := new(service).Apply(context.Background(), wrapperspb.Bytes(wire))
		if status.Code(err) != codes.InvalidArgument {
			t.Fatal("legacy wire reached runtime handler dependencies")
		}
	}
}

func awaitLeaseEvent(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("autonomous lease action did not run")
	}
}

func TestLeaseExpiryAutonomouslyFencesAndWaitsForRealCounterDrain(t *testing.T) {
	g, user, sm, c := fixture(t)
	if _, err := g.apply(context.Background(), c, user, sm); err != nil {
		t.Fatal(err)
	}
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: user, Tag: ManagedInbound})
	ctx = session.ContextWithTimeoutOnly(ctx, true)
	interrupted := make(chan struct{})
	reader := &delayedReader{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, s, err := g.start(ctx, user, func() { reader.Interrupt(); close(interrupted) })
	if err != nil {
		t.Fatal(err)
	}
	counted := dispatcher.WrapLink(ctx, testPolicy{}, sm, &transport.Link{Reader: reader, Writer: discardWriter{}})
	finishedRead := make(chan struct{})
	go func() {
		mb, _ := (&trackedReader{s: s, Reader: counted.Reader}).ReadMultiBuffer()
		buf.ReleaseMulti(mb)
		close(finishedRead)
	}()
	awaitLeaseEvent(t, reader.entered)
	go func() { <-ctx.Done(); s.finish() }()
	renew := c
	renew.Operation = "renew"
	renew.Generation++
	renew.DeadlineBoottimeNS = deadlineAfter(t, g, 250*time.Millisecond)
	if _, err := g.apply(context.Background(), renew, user, sm); err != nil {
		t.Fatal(err)
	}
	awaitLeaseEvent(t, interrupted) // No dispatch/control call triggers this fence.
	g.mu.Lock()
	denied := !g.users[user.Email].allowed
	g.mu.Unlock()
	if !denied {
		t.Fatal("expiry left admission open")
	}
	if _, err := g.apply(context.Background(), renew, user, sm); err == nil {
		t.Fatal("expired exact retry revived lease")
	}
	late := renew
	late.Generation++
	late.DeadlineBoottimeNS = deadlineAfter(t, g, maxLease)
	if _, err := g.apply(context.Background(), late, user, sm); err == nil {
		t.Fatal("late renewal revived lease")
	}
	fence := late
	fence.Operation = "fence"
	fence.DeadlineBoottimeNS = 0
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if r, err := g.apply(short, fence, nil, sm); err == nil || r != nil {
		t.Fatal("expiry certified a still-running counted read")
	}
	cancel()
	replacement := &protocol.MemoryUser{Email: user.Email, Account: &vless.MemoryAccount{}}
	grant := c
	grant.Generation = fence.Generation + 1
	grant.DeadlineBoottimeNS = deadlineAfter(t, g, maxLease)
	if _, err := g.apply(context.Background(), grant, replacement, sm); err == nil {
		t.Fatal("timed-out drain allowed regrant")
	}
	close(reader.release)
	awaitLeaseEvent(t, finishedRead)
	waitEmpty(t, g)
	r, err := g.apply(context.Background(), fence, nil, sm)
	if err != nil || r.Uplink == nil || *r.Uplink != 4 || r.LeaseRemainingMS != nil || r.DeadlineBoottimeNS != 0 {
		t.Fatalf("missing real final receipt after expiry: %v %v", r, err)
	}
	if _, err := g.apply(context.Background(), grant, user, sm); err == nil {
		t.Fatal("old mux identity reopened after expiry")
	}
	if _, err := g.apply(context.Background(), grant, replacement, sm); err != nil {
		t.Fatal(err)
	}
}

func TestSuspendLikeBoottimeAdvanceRejectsDispatchIOAndRenewalWithTimerDelayed(t *testing.T) {
	for _, operation := range []string{"dispatch", "io", "renew"} {
		t.Run(operation, func(t *testing.T) {
			g, user, sm, c := fixture(t)
			clock := freezeLeaseClock(g)
			if _, err := g.apply(context.Background(), c, user, sm); err != nil {
				t.Fatal(err)
			}
			interrupted := make(chan struct{})
			_, s, err := g.start(context.Background(), user, func() { close(interrupted) })
			if err != nil {
				t.Fatal(err)
			}
			g.mu.Lock()
			g.users[user.Email].leaseTimer.Stop()
			g.mu.Unlock()
			advanceLeaseClock(g, clock, 30*time.Second) // Suspend advanced BOOTTIME, not the Go wake hint.
			switch operation {
			case "dispatch":
				if _, _, err := g.start(context.Background(), user, func() {}); err == nil {
					t.Fatal("late dispatch admitted")
				}
			case "io":
				if s.begin() {
					s.end()
					t.Fatal("late counted I/O admitted")
				}
			case "renew":
				c.Operation = "renew"
				c.Generation++
				c.DeadlineBoottimeNS = deadlineAfter(t, g, maxLease)
				if _, err := g.apply(context.Background(), c, user, sm); err == nil {
					t.Fatal("post-suspend renew revived expired lease")
				}
			}
			awaitLeaseEvent(t, interrupted)
			s.finish()
			waitEmpty(t, g)
		})
	}
}

func TestClockReadFailureOrRegressionFencesWithoutUsageFabrication(t *testing.T) {
	for _, failure := range []string{"read-error", "zero", "regression", "timer-read-error"} {
		t.Run(failure, func(t *testing.T) {
			g, user, sm, c := fixture(t)
			clock := freezeLeaseClock(g)
			if _, err := g.apply(context.Background(), c, user, sm); err != nil {
				t.Fatal(err)
			}
			registerPair(t, sm, user.Email)
			interrupted := make(chan struct{})
			_, s, err := g.start(context.Background(), user, func() { close(interrupted) })
			if err != nil {
				t.Fatal(err)
			}
			g.mu.Lock()
			state := g.users[user.Email]
			switch failure {
			case "regression":
				*clock = *clock - 1
			case "zero":
				g.clockNow = func() (int64, error) { return 0, nil }
			default:
				g.clockNow = func() (int64, error) { return 0, errors.New("clock unavailable") }
			}
			g.mu.Unlock()
			if failure == "timer-read-error" {
				g.expireLease(state, c.Generation, c.DeadlineBoottimeNS)
			}
			if s.begin() {
				s.end()
				t.Fatal("clock failure admitted I/O")
			}
			awaitLeaseEvent(t, interrupted)
			s.finish()
			waitEmpty(t, g)
			renew := c
			renew.Operation = "renew"
			renew.Generation++
			if _, err := g.apply(context.Background(), renew, user, sm); err == nil {
				t.Fatal("unavailable clock renewed lease")
			}
			// Explicit revoke still drains and reports actual existing counters;
			// it does not need to grant time authority from an unavailable clock.
			fence := renew
			fence.Operation = "fence"
			fence.DeadlineBoottimeNS = 0
			r, err := g.apply(context.Background(), fence, nil, sm)
			if err != nil || r.State != "fenced" || r.Uplink == nil || *r.Uplink != 0 || r.Downlink == nil || *r.Downlink != 0 {
				t.Fatalf("clock failure fabricated/lost final counters: %v %v", r, err)
			}
		})
	}
}

func TestCloseStopsLeaseTimerAndCannotBeRenewed(t *testing.T) {
	g, user, sm, c := fixture(t)
	if _, err := g.apply(context.Background(), c, user, sm); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	state := g.users[user.Email]
	deadline := state.leaseDeadlineNS
	g.mu.Unlock()
	g.close()
	g.mu.Lock()
	stopped := state.leaseTimer == nil && !state.allowed
	g.mu.Unlock()
	if !stopped {
		t.Fatal("close left lease timer/admission active")
	}
	g.expireLease(state, c.Generation, deadline)
	c.Operation = "renew"
	c.Generation++
	if _, err := g.apply(context.Background(), c, user, sm); err == nil {
		t.Fatal("closed runtime renewed lease")
	}
}
