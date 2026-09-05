package runtimefence

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	xstats "github.com/xtls/xray-core/app/stats"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/transport"
)

type testPolicy struct{ policy.Manager }

func (testPolicy) ForLevel(uint32) policy.Session {
	p := policy.SessionDefault()
	p.Stats = policy.Stats{UserUplink: true, UserDownlink: true}
	return p
}

func fixture(t *testing.T) (*gate, *protocol.MemoryUser, *xstats.Manager, Control) {
	t.Helper()
	g, err := newGate(strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	u := &protocol.MemoryUser{Email: "wl:synthetic:exit-s1", Account: &vless.MemoryAccount{}}
	sm, err := xstats.NewManager(context.Background(), &xstats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	c := Control{Schema: 2, Operation: "grant", Email: u.Email, BootID: g.boot, ConfigDigest: g.digest, Generation: 1, ClockDomain: g.clockDomain, DeadlineBoottimeNS: deadlineAfter(t, g, maxLease)}
	t.Cleanup(g.close)
	return g, u, sm, c
}

func deadlineAfter(t *testing.T, g *gate, remaining time.Duration) int64 {
	t.Helper()
	g.mu.Lock()
	now, err := g.nowLocked()
	g.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return now + int64(remaining)
}

func registerPair(t *testing.T, sm stats.Manager, email string) {
	t.Helper()
	for _, direction := range []string{"uplink", "downlink"} {
		if _, err := stats.GetOrRegisterCounter(sm, counterName(email, direction)); err != nil {
			t.Fatal(err)
		}
	}
}

func waitEmpty(t *testing.T, g *gate) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		g.mu.Lock()
		count, ch := g.count, g.changed
		g.mu.Unlock()
		if count == 0 {
			return
		}
		select {
		case <-ch:
		case <-timer.C:
			t.Fatal("session did not drain")
		}
	}
}

func TestDefaultDenyGenerationAndOldMuxIdentity(t *testing.T) {
	g, u, sm, c := fixture(t)
	if _, _, err := g.start(context.Background(), u, func() {}); err == nil {
		t.Fatal("default allowed")
	}
	wrong := c
	wrong.BootID = strings.Repeat("c", 64)
	if _, err := g.apply(context.Background(), wrong, u, sm); err == nil {
		t.Fatal("wrong boot allowed")
	}
	if _, err := g.apply(context.Background(), c, u, sm); err != nil {
		t.Fatal(err)
	}
	registerPair(t, sm, u.Email)
	c.Operation = "fence"
	c.DeadlineBoottimeNS = 0
	c.Generation++
	r, err := g.apply(context.Background(), c, nil, sm)
	if err != nil || r.Uplink == nil || *r.Uplink != 0 || r.ResetSequence != 0 {
		t.Fatalf("final receipt: %v, %v", r, err)
	}
	grant := c
	grant.Operation = "grant"
	grant.DeadlineBoottimeNS = deadlineAfter(t, g, maxLease)
	if _, err := g.apply(context.Background(), grant, u, sm); err == nil {
		t.Fatal("same generation changed operation")
	}
	grant.Generation++
	if _, err := g.apply(context.Background(), grant, u, sm); err == nil {
		t.Fatal("old authenticated mux identity rearmed")
	}
	replacement := &protocol.MemoryUser{Email: u.Email, Account: &vless.MemoryAccount{}}
	if _, err := g.apply(context.Background(), grant, replacement, sm); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.start(context.Background(), u, func() {}); err == nil {
		t.Fatal("old mux inherited new grant")
	}
	if _, err := g.apply(context.Background(), c, nil, sm); err == nil {
		t.Fatal("stale fence accepted")
	}
}

type delayedReader struct{ entered, release chan struct{} }

func (r *delayedReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	close(r.entered)
	<-r.release
	b := buf.New()
	_, _ = b.Write([]byte("late"))
	return buf.MultiBuffer{b}, nil
}
func (*delayedReader) Interrupt() {}

type discardWriter struct{}

func (discardWriter) WriteMultiBuffer(mb buf.MultiBuffer) error { buf.ReleaseMulti(mb); return nil }

func TestFenceWaitsForLatePinnedCounterMutationAfterWorkerReturn(t *testing.T) {
	g, u, sm, c := fixture(t)
	if _, err := g.apply(context.Background(), c, u, sm); err != nil {
		t.Fatal(err)
	}
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: u, Tag: ManagedInbound})
	// XUDP intentionally ignores ordinary cancellation; the reference must
	// survive worker return and a non-cooperating underlying Read/Interrupt.
	ctx = session.ContextWithTimeoutOnly(ctx, true)
	reader := &delayedReader{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, s, err := g.start(ctx, u, func() { reader.Interrupt() })
	if err != nil {
		t.Fatal(err)
	}
	counted := dispatcher.WrapLink(ctx, testPolicy{}, sm, &transport.Link{Reader: reader, Writer: discardWriter{}})
	outer := &buf.TimeoutWrapperReader{Reader: &trackedReader{s: s, Reader: counted.Reader}}
	if _, err := outer.ReadMultiBufferTimeout(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	<-reader.entered
	s.finish()
	c.Operation = "fence"
	c.DeadlineBoottimeNS = 0
	c.Generation++
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if r, err := g.apply(short, c, nil, sm); err == nil || r != nil {
		t.Fatal("receipt escaped outstanding counted read")
	}
	cancel()
	if _, _, err := g.start(context.Background(), u, func() {}); err == nil {
		t.Fatal("timeout reopened admission")
	}
	close(reader.release)
	mb, err := outer.ReadMultiBuffer()
	if err != nil {
		t.Fatal(err)
	}
	buf.ReleaseMulti(mb)
	waitEmpty(t, g)
	r, err := g.apply(context.Background(), c, nil, sm)
	if err != nil || r.Uplink == nil || *r.Uplink != 4 {
		t.Fatalf("late real UP omitted: %v, %v", r, err)
	}
	if _, err := (&trackedReader{s: s, Reader: reader}).ReadMultiBuffer(); err == nil {
		t.Fatal("closed stream admitted late read")
	}
}

func TestNeverStartedFenceReturnsUnusedWithoutCounterSample(t *testing.T) {
	for _, granted := range []bool{false, true} {
		name := "never_granted"
		if granted {
			name = "granted_but_never_started"
		}
		t.Run(name, func(t *testing.T) {
			g, u, sm, c := fixture(t)
			if _, _, err := g.start(context.Background(), u, func() {}); err == nil {
				t.Fatal("ungranted start accepted")
			}
			if granted {
				if _, err := g.apply(context.Background(), c, u, sm); err != nil {
					t.Fatal(err)
				}
				c.Generation++
			}
			c.Operation, c.DeadlineBoottimeNS = "fence", 0
			for attempt := 0; attempt < 2; attempt++ {
				r, err := g.apply(context.Background(), c, nil, sm)
				if err != nil || r == nil || r.State != "fenced_unused" || r.Uplink != nil || r.Downlink != nil || r.LeaseRemainingMS != nil || r.DeadlineBoottimeNS != 0 || r.BootID != g.boot || r.Generation != c.Generation || r.ClockDomain != g.clockDomain {
					t.Fatalf("invalid unused receipt: %v %v", r, err)
				}
			}
			if sm.GetCounter(counterName(u.Email, "uplink")) != nil || sm.GetCounter(counterName(u.Email, "downlink")) != nil {
				t.Fatal("unused fence created zero counters")
			}
		})
	}
}

func TestPartialCounterPairCannotBeCertifiedUnused(t *testing.T) {
	for _, direction := range []string{"uplink", "downlink"} {
		t.Run(direction, func(t *testing.T) {
			g, u, sm, c := fixture(t)
			if _, err := sm.RegisterCounter(counterName(u.Email, direction)); err != nil {
				t.Fatal(err)
			}
			c.Operation, c.DeadlineBoottimeNS = "fence", 0
			if r, err := g.apply(context.Background(), c, nil, sm); err == nil || r != nil {
				t.Fatal("partial counters certified as unused")
			}
		})
	}
}

func TestSuccessfulStartPermanentlyForbidsUnusedReceiptForPhysicalBoot(t *testing.T) {
	g, u, sm, c := fixture(t)
	if _, err := g.apply(context.Background(), c, u, sm); err != nil {
		t.Fatal(err)
	}
	_, s, err := g.start(context.Background(), u, func() {})
	if err != nil {
		t.Fatal(err)
	}
	s.finish()
	waitEmpty(t, g)
	c.Operation, c.DeadlineBoottimeNS = "fence", 0
	c.Generation++
	if r, err := g.apply(context.Background(), c, nil, sm); err == nil || r != nil {
		t.Fatal("missing counters after successful dispatch certified unused")
	}
	// Replacing the MemoryUser does not create a new physical counter lifetime.
	replacement := &protocol.MemoryUser{Email: u.Email, Account: &vless.MemoryAccount{}}
	c.Operation, c.DeadlineBoottimeNS = "grant", deadlineAfter(t, g, maxLease)
	c.Generation++
	if _, err := g.apply(context.Background(), c, replacement, sm); err != nil {
		t.Fatal(err)
	}
	c.Operation, c.DeadlineBoottimeNS = "fence", 0
	c.Generation++
	if r, err := g.apply(context.Background(), c, nil, sm); err == nil || r != nil {
		t.Fatal("regrant erased successful-start history")
	}
}

func TestRealZeroCounterPairProducesOrdinaryFencedReceipt(t *testing.T) {
	g, u, sm, c := fixture(t)
	registerPair(t, sm, u.Email)
	c.Operation, c.DeadlineBoottimeNS = "fence", 0
	r, err := g.apply(context.Background(), c, nil, sm)
	if err != nil || r == nil || r.State != "fenced" || r.Uplink == nil || r.Downlink == nil || *r.Uplink != 0 || *r.Downlink != 0 {
		t.Fatalf("real zero pair lost: %v %v", r, err)
	}
}

func TestNegativeCountersDoNotProduceFinalReceipt(t *testing.T) {
	g, u, sm, c := fixture(t)
	c.Operation = "fence"
	c.DeadlineBoottimeNS = 0
	registerPair(t, sm, u.Email)
	sm.GetCounter(counterName(u.Email, "uplink")).Add(-1)
	if r, err := g.apply(context.Background(), c, nil, sm); err == nil || r != nil {
		t.Fatal("negative counter certified")
	}
}
