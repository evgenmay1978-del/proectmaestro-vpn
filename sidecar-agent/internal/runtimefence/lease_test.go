package runtimefence

import (
	"context"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/transport"
)

func TestLeaseBoundsRenewalAndNonExtendingExactRetry(t *testing.T) {
	g, user, sm, c := fixture(t)
	for _, ms := range []uint32{0, 5001, ^uint32(0)} {
		bad := c
		bad.LeaseMS = ms
		if r, err := g.apply(context.Background(), bad, user, sm); err == nil || r != nil {
			t.Fatalf("invalid lease length accepted: %d", ms)
		}
	}
	bad := c
	bad.Operation = "renew"
	if _, err := g.apply(context.Background(), bad, user, sm); err == nil {
		t.Fatal("renew created an initial grant")
	}
	bad.Operation = "fence"
	if _, err := g.apply(context.Background(), bad, nil, sm); err == nil {
		t.Fatal("fence accepted a lease duration")
	}
	first, err := g.apply(context.Background(), c, user, sm)
	if err != nil {
		t.Fatal(err)
	}
	if first.LeaseRemainingMS == nil || *first.LeaseRemainingMS > 5000 || first.LeaseExpiresAt == "" {
		t.Fatal("missing or unbounded lease receipt")
	}
	if _, err := time.Parse(time.RFC3339Nano, first.LeaseExpiresAt); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	state := g.users[user.Email]
	originalDeadline, originalTimer := state.leaseDeadline, state.leaseTimer
	g.mu.Unlock()
	duplicate, err := g.apply(context.Background(), c, user, sm)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.LeaseExpiresAt != first.LeaseExpiresAt || duplicate.LeaseRemainingMS == nil || *duplicate.LeaseRemainingMS > *first.LeaseRemainingMS {
		t.Fatal("duplicate extended lease receipt")
	}
	bad = c
	bad.LeaseMS--
	if _, err := g.apply(context.Background(), bad, user, sm); err == nil {
		t.Fatal("changed retry lease accepted")
	}
	g.mu.Lock()
	unchanged := state.leaseDeadline.Equal(originalDeadline) && state.leaseTimer == originalTimer
	g.mu.Unlock()
	if !unchanged {
		t.Fatal("retry rearmed the lease timer")
	}
	renew := c
	renew.Operation = "renew"
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
	renewedDeadline, renewedTimer := state.leaseDeadline, state.leaseTimer
	g.mu.Unlock()
	if renewedDeadline.Before(originalDeadline) || renewedTimer == originalTimer {
		t.Fatal("fresh renewal did not replace lease timer")
	}
	duplicate, err = g.apply(context.Background(), renew, user, sm)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.LeaseExpiresAt != renewed.LeaseExpiresAt || *duplicate.LeaseRemainingMS > *renewed.LeaseRemainingMS {
		t.Fatal("renew retry extended lease")
	}
	if _, err := g.apply(context.Background(), c, user, sm); err == nil {
		t.Fatal("older grant replay accepted after renewal")
	}
	g.expireLease(state, c.Generation, originalDeadline)
	g.mu.Lock()
	stillAllowed := state.allowed && state.leaseDeadline.Equal(renewedDeadline)
	g.mu.Unlock()
	if !stillAllowed {
		t.Fatal("stale timer callback changed renewed lease")
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
	// Arm the short lease only after the real counted read is already blocked.
	renew := c
	renew.Operation = "renew"
	renew.Generation++
	renew.LeaseMS = 20
	if _, err := g.apply(context.Background(), renew, user, sm); err != nil {
		t.Fatal(err)
	}
	awaitLeaseEvent(t, interrupted) // No dispatch/control call triggers this fence.
	g.mu.Lock()
	state := g.users[user.Email]
	denied := !state.allowed
	g.mu.Unlock()
	if !denied {
		t.Fatal("expiry left admission open")
	}
	if _, err := g.apply(context.Background(), renew, user, sm); err == nil {
		t.Fatal("expired exact retry revived lease")
	}
	late := renew
	late.Generation++
	if _, err := g.apply(context.Background(), late, user, sm); err == nil {
		t.Fatal("late renewal revived lease")
	}
	fence := late
	fence.Operation = "fence"
	fence.LeaseMS = 0
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if r, err := g.apply(short, fence, nil, sm); err == nil || r != nil {
		t.Fatal("expiry certified a still-running counted read")
	}
	cancel()
	replacement := &protocol.MemoryUser{Email: user.Email, Account: &vless.MemoryAccount{}}
	grant := c
	grant.Generation = fence.Generation + 1
	if _, err := g.apply(context.Background(), grant, replacement, sm); err == nil {
		t.Fatal("timed-out drain allowed regrant")
	}
	close(reader.release)
	awaitLeaseEvent(t, finishedRead)
	waitEmpty(t, g)
	r, err := g.apply(context.Background(), fence, nil, sm)
	if err != nil || r.Uplink == nil || *r.Uplink != 4 || r.LeaseRemainingMS != nil || r.LeaseExpiresAt != "" {
		t.Fatalf("missing real final receipt after expiry: %v %v", r, err)
	}
	if _, err := g.apply(context.Background(), grant, user, sm); err == nil {
		t.Fatal("old mux identity reopened after expiry")
	}
	if _, err := g.apply(context.Background(), grant, replacement, sm); err != nil {
		t.Fatal(err)
	}
}

func TestDeadlineGuardsRejectLateDispatchIOAndRenewalWithoutTimer(t *testing.T) {
	for _, operation := range []string{"dispatch", "io", "renew"} {
		t.Run(operation, func(t *testing.T) {
			g, user, sm, c := fixture(t)
			if _, err := g.apply(context.Background(), c, user, sm); err != nil {
				t.Fatal(err)
			}
			interrupted := make(chan struct{})
			_, s, err := g.start(context.Background(), user, func() { close(interrupted) })
			if err != nil {
				t.Fatal(err)
			}
			// Model a scheduler-delayed callback: admission must enforce its own
			// deadline. Only the test changes the lease clock; no usage is forged.
			g.mu.Lock()
			state := g.users[user.Email]
			state.leaseTimer.Stop()
			state.leaseDeadline = time.Now().Add(-time.Millisecond)
			g.mu.Unlock()
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
				if _, err := g.apply(context.Background(), c, user, sm); err == nil {
					t.Fatal("late renew outran expiry callback")
				}
			}
			awaitLeaseEvent(t, interrupted)
			s.finish()
			waitEmpty(t, g)
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
	deadline := state.leaseDeadline
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
