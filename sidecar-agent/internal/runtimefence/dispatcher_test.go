package runtimefence

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type downstream struct {
	routing.Dispatcher
	run func(context.Context, *transport.Link) error
}

func (d downstream) DispatchLink(ctx context.Context, _ xnet.Destination, l *transport.Link) error {
	return d.run(ctx, l)
}

type parentConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *parentConn) Close() error { c.closes.Add(1); return nil }
func payload(text string) buf.MultiBuffer {
	b := buf.New()
	_, _ = b.Write([]byte(text))
	return buf.MultiBuffer{b}
}

func TestDispatchKeepsQueuedUplinkAccountingAndRawXUDPReader(t *testing.T) {
	g, u, sm, c := fixture(t)
	if _, err := g.apply(context.Background(), c, u, sm); err != nil {
		t.Fatal(err)
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	d := &Dispatcher{gate: g, stats: sm, policy: testPolicy{}, managed: downstream{run: func(context.Context, *transport.Link) error { close(blocked); <-release; return nil }}}
	conn := new(parentConn)
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: u, Tag: ManagedInbound, Conn: conn})
	ctx = session.ContextWithTimeoutOnly(ctx, true)
	l, err := d.Dispatch(ctx, xnet.TCPDestination(xnet.LocalHostIP, 443))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.Reader.(*pipe.Reader); !ok {
		t.Fatal("XUDP reader type changed")
	}
	<-blocked
	if err := l.Writer.WriteMultiBuffer(payload("queued-up")); err != nil {
		t.Fatal(err)
	}
	if got := sm.GetCounter(counterName(u.Email, "uplink")).Value(); got != 9 {
		t.Fatalf("UP counted at consumption instead of write: %d", got)
	}
	c.Operation = "fence"
	c.Generation++
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if r, err := g.apply(short, c, nil, sm); err == nil || r != nil {
		t.Fatal("dial-blocked worker not drained")
	}
	cancel()
	close(release)
	waitEmpty(t, g)
	r, err := g.apply(context.Background(), c, nil, sm)
	if err != nil || *r.Uplink != 9 {
		t.Fatalf("final UP lost: %v %v", r, err)
	}
	if conn.closes.Load() == 0 {
		t.Fatal("explicit fence did not close parent stream")
	}
	if err := l.Writer.WriteMultiBuffer(payload("late")); err == nil {
		t.Fatal("fenced XUDP resume wrote")
	}
}

func TestNormalChildFinishPreservesParentMuxAndQueuedDownlink(t *testing.T) {
	g, u, sm, c := fixture(t)
	if _, err := g.apply(context.Background(), c, u, sm); err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{gate: g, stats: sm, policy: testPolicy{}, managed: downstream{run: func(_ context.Context, l *transport.Link) error {
		if err := l.Writer.WriteMultiBuffer(payload("response")); err != nil {
			return err
		}
		return common.Close(l.Writer)
	}}}
	conn := new(parentConn)
	ctx := session.ContextWithInbound(context.Background(), &session.Inbound{User: u, Tag: ManagedInbound, Conn: conn})
	l, err := d.Dispatch(ctx, xnet.TCPDestination(xnet.LocalHostIP, 443))
	if err != nil {
		t.Fatal(err)
	}
	waitEmpty(t, g)
	if conn.closes.Load() != 0 {
		t.Fatal("one completed child killed shared mux")
	}
	mb, err := l.Reader.ReadMultiBuffer()
	if err != nil || mb.Len() != 8 {
		t.Fatalf("queued response discarded: %d %v", mb.Len(), err)
	}
	buf.ReleaseMulti(mb)
	c.Operation = "fence"
	c.Generation++
	r, err := g.apply(context.Background(), c, nil, sm)
	if err != nil || *r.Downlink != 8 {
		t.Fatalf("real DOWN: %v %v", r, err)
	}
}

func TestManagedDelegateCannotDuplicateUserCounters(t *testing.T) {
	p := noUserStatsPolicy{testPolicy{}}.ForLevel(0)
	if p.Stats.UserUplink || p.Stats.UserDownlink || p.Stats.UserOnline {
		t.Fatal("delegate enables a second user counter layer")
	}
	g, u, sm, c := fixture(t)
	if _, err := g.apply(context.Background(), c, u, sm); err != nil {
		t.Fatal(err)
	}
	called := false
	d := &Dispatcher{gate: g, stats: sm, policy: testPolicy{}, ordinary: downstream{run: func(context.Context, *transport.Link) error { called = true; return nil }}}
	if err := d.DispatchLink(context.Background(), xnet.TCPDestination(xnet.LocalHostIP, 443), &transport.Link{}); err != nil || !called {
		t.Fatal("ordinary dispatcher fallback changed")
	}
}
