package runtimefence

import (
	"context"
	"io"
	"strings"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

type noUserStatsPolicy struct{ policy.Manager }

func (p noUserStatsPolicy) ForLevel(level uint32) policy.Session {
	s := p.Manager.ForLevel(level)
	s.Stats = policy.Stats{}
	return s
}

type Dispatcher struct {
	gate     *gate
	managed  routing.Dispatcher
	ordinary routing.Dispatcher
	policy   policy.Manager
	stats    stats.Manager
}

func (*Dispatcher) Type() interface{} { return routing.DispatcherType() }
func (d *Dispatcher) Start() error    { return nil }
func (d *Dispatcher) Close() error    { d.gate.close(); return nil }

func (d *Dispatcher) identity(ctx context.Context) (*protocol.MemoryUser, bool, error) {
	in := session.InboundFromContext(ctx)
	if in == nil || in.User == nil || !strings.HasPrefix(in.User.Email, "wl:") {
		return nil, false, nil
	}
	u := in.User
	a, ok := u.Account.(*vless.MemoryAccount)
	if !validEmail(u.Email) || in.Tag != ManagedInbound || !ok || a.Reverse != nil || a.Flow != "" {
		return nil, true, errDenied
	}
	p := d.policy.ForLevel(u.Level)
	if !p.Stats.UserUplink || !p.Stats.UserDownlink || p.Stats.UserOnline {
		return nil, true, errDenied
	}
	if content := session.ContentFromContext(ctx); content != nil && content.SniffingRequest.Enabled {
		return nil, true, errDenied
	}
	return u, true, nil
}

func interruptLink(ctx context.Context, link *transport.Link) func() {
	in := session.InboundFromContext(ctx)
	return func() {
		common.Interrupt(link.Reader)
		common.Interrupt(link.Writer)
		if in != nil && in.Conn != nil {
			_ = in.Conn.Close()
		}
	}
}

func (d *Dispatcher) DispatchLink(ctx context.Context, dest xnet.Destination, link *transport.Link) error {
	u, managed, err := d.identity(ctx)
	if err != nil {
		return err
	}
	if !managed {
		return d.ordinary.DispatchLink(ctx, dest, link)
	}
	if !dest.IsValid() || link == nil || link.Reader == nil || link.Writer == nil {
		return errDenied
	}
	ctx, s, err := d.gate.start(ctx, u, interruptLink(ctx, link))
	if err != nil {
		return err
	}
	defer s.finish()
	counted := dispatcher.WrapLink(ctx, d.policy, d.stats, &transport.Link{Reader: link.Reader, Writer: link.Writer})
	tracked := &transport.Link{Reader: &trackedReader{s: s, Reader: counted.Reader}, Writer: &trackedWriter{s: s, Writer: counted.Writer}}
	return d.managed.DispatchLink(ctx, dest, tracked)
}

func (d *Dispatcher) Dispatch(ctx context.Context, dest xnet.Destination) (*transport.Link, error) {
	u, managed, err := d.identity(ctx)
	if err != nil {
		return nil, err
	}
	if !managed {
		return d.ordinary.Dispatch(ctx, dest)
	}
	if !dest.IsValid() {
		return nil, errDenied
	}
	ur, uw := pipe.New(pipe.OptionsFromContext(ctx)...)
	dr, dw := pipe.New(pipe.OptionsFromContext(ctx)...)
	outside := &transport.Link{Reader: dr, Writer: uw}
	inside := &transport.Link{Reader: ur, Writer: dw}
	interrupt := interruptLink(ctx, inside)
	ctx, s, err := d.gate.start(ctx, u, func() { interrupt(); common.Interrupt(dr); common.Interrupt(uw) })
	if err != nil {
		common.Interrupt(ur)
		common.Interrupt(uw)
		common.Interrupt(dr)
		common.Interrupt(dw)
		return nil, err
	}
	up, eu := stats.GetOrRegisterCounter(d.stats, counterName(u.Email, "uplink"))
	down, ed := stats.GetOrRegisterCounter(d.stats, counterName(u.Email, "downlink"))
	if eu != nil || ed != nil || up == nil || down == nil {
		s.finish()
		return nil, errDenied
	}
	// Match pinned getLink: count UP before the inbound pipe write, not when
	// its reader eventually consumes bytes. XUDP requires outside.Reader to
	// remain exactly *pipe.Reader, including when its mux session is resumed.
	outside.Writer = &trackedWriter{s: s, Writer: &dispatcher.SizeStatWriter{Counter: up, Writer: uw}}
	inside.Reader = &trackedReader{s: s, Reader: ur}
	inside.Writer = &trackedWriter{s: s, Writer: &dispatcher.SizeStatWriter{Counter: down, Writer: dw}}
	go func() { defer s.finish(); _ = d.managed.DispatchLink(ctx, dest, inside) }()
	return outside, nil
}

type trackedReader struct {
	s *stream
	buf.Reader
}

func (r *trackedReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	if !r.s.begin() {
		return nil, io.ErrClosedPipe
	}
	defer r.s.end()
	return r.Reader.ReadMultiBuffer()
}
func (r *trackedReader) Interrupt()   { common.Interrupt(r.Reader) }
func (r *trackedReader) Close() error { return common.Close(r.Reader) }

type trackedWriter struct {
	s *stream
	buf.Writer
}

func (w *trackedWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	if !w.s.begin() {
		buf.ReleaseMulti(mb)
		return io.ErrClosedPipe
	}
	defer w.s.end()
	return w.Writer.WriteMultiBuffer(mb)
}
func (w *trackedWriter) Interrupt()   { common.Interrupt(w.Writer) }
func (w *trackedWriter) Close() error { return common.Close(w.Writer) }
