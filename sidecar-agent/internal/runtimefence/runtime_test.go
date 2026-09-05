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
	g := newGate(strings.Repeat("a", 64), strings.Repeat("b", 64))
	u := &protocol.MemoryUser{Email: "wl:synthetic:exit-s1", Account: &vless.MemoryAccount{}}
	sm, err := xstats.NewManager(context.Background(), &xstats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	c := Control{Schema: 1, Operation: "grant", Email: u.Email, BootID: g.boot, ConfigDigest: g.digest, Generation: 1}
	t.Cleanup(g.close)
	return g, u, sm, c
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
	c.Generation++
	r, err := g.apply(context.Background(), c, nil, sm)
	if err != nil || r.Uplink == nil || *r.Uplink != 0 || r.ResetSequence != 0 {
		t.Fatalf("final receipt: %v, %v", r, err)
	}
	grant := c
	grant.Operation = "grant"
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

func TestMissingOrOverflowedCountersDoNotProduceFinalReceipt(t *testing.T) {
	g, u, sm, c := fixture(t)
	c.Operation = "fence"
	if r, err := g.apply(context.Background(), c, nil, sm); err == nil || r != nil {
		t.Fatal("invented absent counters")
	}
	registerPair(t, sm, u.Email)
	sm.GetCounter(counterName(u.Email, "uplink")).Add(-1)
	if r, err := g.apply(context.Background(), c, nil, sm); err == nil || r != nil {
		t.Fatal("negative counter certified")
	}
}
