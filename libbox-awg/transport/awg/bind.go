package awg

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

var _ conn.Bind = (*bind_adapter)(nil)

// bind_adapter carries the outer (encrypted) AWG traffic over a sing-box dialer.
//
// amneziawg-go's own conn.NewStdNetBind() accepts no control functions — its controlFns is a
// package global filled in only by the engine's own platform init — so there is no hook for
// VpnService.protect() and its sockets would be captured by our own tun on Android. Upstream
// sing-box hits the same wall for WireGuard-over-detour and solves it with
// transport/wireguard.ClientBind; this is that design ported onto the amneziawg Bind
// interface, and it has to keep the two properties that make it work:
//
//   - The socket is opened LAZILY, on the first send or receive — never in Open(). Open() runs
//     inside box start, before the tun inbound exists and before the interface monitor has
//     settled on a default interface, and on Android dialer.ListenPacket then picks the
//     interface itself (platformInterface makes networkStrategy non-nil). A socket opened that
//     early binds against a half-built view of the network and silently blackholes every
//     packet afterwards, with no error anywhere: the engine reports the handshake as sent and
//     nothing reaches the wire.
//   - A socket error CLOSES the socket so the next call builds a fresh one, instead of being
//     returned upwards. A ReceiveFunc that returns an error stops the engine's receive loop
//     for good, so a single transient read error would otherwise end the tunnel permanently.
type bind_adapter struct {
	ctx          context.Context
	logger       logger.Logger
	pauseManager pause.Manager
	dialer       N.Dialer
	isConnect    bool
	connectAddr  netip.AddrPort
	bindCtx      context.Context
	bindDone     context.CancelFunc
	connAccess   sync.Mutex
	conn         *wireConn
	done         chan struct{}
}

func newBind(ctx context.Context, logger logger.Logger, dial N.Dialer, isConnect bool, connectAddr netip.AddrPort) conn.Bind {
	return &bind_adapter{
		ctx:          ctx,
		logger:       logger,
		pauseManager: service.FromContext[pause.Manager](ctx),
		dialer:       dial,
		isConnect:    isConnect,
		connectAddr:  connectAddr,
		done:         make(chan struct{}),
	}
}

func (b *bind_adapter) waitActive() {
	if b.pauseManager != nil {
		b.pauseManager.WaitActive()
	}
}

func (b *bind_adapter) connect() (*wireConn, error) {
	serverConn := b.conn
	if serverConn != nil {
		select {
		case <-serverConn.done:
			serverConn = nil
		default:
			return serverConn, nil
		}
	}
	b.connAccess.Lock()
	defer b.connAccess.Unlock()
	select {
	case <-b.done:
		return nil, net.ErrClosed
	default:
	}
	serverConn = b.conn
	if serverConn != nil {
		select {
		case <-serverConn.done:
			serverConn = nil
		default:
			return serverConn, nil
		}
	}
	bindCtx := b.bindCtx
	if bindCtx == nil {
		bindCtx = b.ctx
	}
	if b.isConnect {
		// A connected UDP socket to the single literal peer: the same dial path every other
		// protocol in this app already uses successfully on Android.
		udpConn, err := b.dialer.DialContext(bindCtx, N.NetworkUDP, M.SocksaddrFromNetIP(b.connectAddr))
		if err != nil {
			return nil, err
		}
		b.conn = &wireConn{
			PacketConn: bufio.NewUnbindPacketConn(udpConn),
			done:       make(chan struct{}),
		}
	} else {
		udpConn, err := b.dialer.ListenPacket(bindCtx, M.Socksaddr{Addr: netip.IPv4Unspecified()})
		if err != nil {
			return nil, err
		}
		b.conn = &wireConn{
			PacketConn: bufio.NewPacketConn(udpConn),
			done:       make(chan struct{}),
		}
	}
	return b.conn, nil
}

func (b *bind_adapter) Open(port uint16) (fns []conn.ReceiveFunc, actualPort uint16, err error) {
	b.connAccess.Lock()
	defer b.connAccess.Unlock()
	select {
	case <-b.done:
		b.done = make(chan struct{})
	default:
	}
	b.bindCtx, b.bindDone = context.WithCancel(b.ctx)
	return []conn.ReceiveFunc{b.receive}, 0, nil
}

func (b *bind_adapter) receive(packets [][]byte, sizes []int, eps []conn.Endpoint) (count int, err error) {
	udpConn, err := b.connect()
	if err != nil {
		select {
		case <-b.done:
			return
		default:
		}
		b.logger.Error(E.Cause(err, "connect to server"))
		err = nil
		b.waitActive()
		time.Sleep(time.Second)
		return
	}
	n, addr, err := udpConn.ReadFrom(packets[0])
	if err != nil {
		udpConn.Close()
		select {
		case <-b.done:
		default:
			b.logger.Error(E.Cause(err, "read packet"))
			err = nil
		}
		return
	}
	sizes[0] = n
	eps[0] = bind_endpoint(M.SocksaddrFromNet(addr).Unwrap().AddrPort())
	count = 1
	return
}

func (b *bind_adapter) Close() error {
	b.connAccess.Lock()
	defer b.connAccess.Unlock()
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	if b.bindDone != nil {
		b.bindDone()
		b.bindDone = nil
	}
	common.Close(common.PtrOrNil(b.conn))
	b.conn = nil
	return nil
}

func (b *bind_adapter) SetMark(mark uint32) error {
	return nil
}

func (b *bind_adapter) Send(bufs [][]byte, ep conn.Endpoint) error {
	udpConn, err := b.connect()
	if err != nil {
		b.waitActive()
		time.Sleep(time.Second)
		return err
	}
	destination, err := endpointAddrPort(ep)
	if err != nil {
		return err
	}
	for _, buf := range bufs {
		if _, err = udpConn.WriteToUDPAddrPort(buf, destination); err != nil {
			udpConn.Close()
			return err
		}
	}
	return nil
}

func (b *bind_adapter) ParseEndpoint(s string) (conn.Endpoint, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, E.Cause(err, "parse addrport")
	}
	return bind_endpoint(ap), nil
}

func (b *bind_adapter) BatchSize() int {
	return 1
}

func endpointAddrPort(ep conn.Endpoint) (netip.AddrPort, error) {
	switch endpoint := ep.(type) {
	case bind_endpoint:
		return netip.AddrPort(endpoint), nil
	case *bind_endpoint:
		return netip.AddrPort(*endpoint), nil
	default:
		return netip.AddrPort{}, E.New("unexpected endpoint type: ", ep.DstToString())
	}
}

type wireConn struct {
	net.PacketConn
	access sync.Mutex
	done   chan struct{}
}

func (w *wireConn) WriteToUDPAddrPort(b []byte, addr netip.AddrPort) (int, error) {
	return w.PacketConn.WriteTo(b, M.SocksaddrFromNetIP(addr).UDPAddr())
}

func (w *wireConn) Close() error {
	w.access.Lock()
	defer w.access.Unlock()
	select {
	case <-w.done:
		return net.ErrClosed
	default:
	}
	w.PacketConn.Close()
	close(w.done)
	return nil
}

var _ conn.Endpoint = (*bind_endpoint)(nil)

type bind_endpoint netip.AddrPort

func (e bind_endpoint) ClearSrc() {}

func (e bind_endpoint) SrcToString() string {
	return ""
}

func (e bind_endpoint) DstToString() string {
	return (netip.AddrPort)(e).String()
}

func (e bind_endpoint) DstToBytes() []byte {
	b, _ := (netip.AddrPort)(e).MarshalBinary()
	return b
}

func (e bind_endpoint) DstIP() netip.Addr {
	return (netip.AddrPort)(e).Addr()
}

func (e bind_endpoint) SrcIP() netip.Addr {
	return netip.Addr{}
}
