package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	"github.com/sagernet/sing/common/varbin"
	"github.com/sagernet/sing/protocol/socks/socks5"
	xnet "github.com/xtls/xray-core/common/net"
)

const (
	nativeHandshakeTimeout = 10 * time.Second
	nativeIdleTimeout      = 60 * time.Second
	nativeWriteTimeout     = 10 * time.Second
	nativeStopTimeout      = 3 * time.Second
	maxAcceptedConnections = 32
	maxUDPLinks            = 64
)

var errBoundary = errors.New("native_boundary_failed")

// ownedConn makes Close idempotent even when the upstream virtual connection
// is not. It also cancels the corresponding dispatch before closing its pipes.
type ownedConn struct {
	net.Conn
	once    sync.Once
	onClose func()
}

func (c *ownedConn) Close() error {
	c.once.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
		_ = c.Conn.Close()
	})
	return nil
}

type socksServer struct {
	ctx            context.Context
	cancel         context.CancelFunc
	listener       net.Listener
	core           runningInstance
	user, password string
	mu             sync.Mutex
	closed         bool
	connections    map[*ownedConn]struct{}
	accepted       chan struct{}
	udpLinks       chan struct{}
	wg             sync.WaitGroup
	closeOnce      sync.Once
	closeDone      chan struct{}
	closeErr       error
}

func newSOCKSServer(parent context.Context, value transport, instance runningInstance) (*socksServer, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(value.SocksPort)))
	if err != nil {
		return nil, errBoundary
	}
	ctx, cancel := context.WithCancel(parent)
	s := &socksServer{
		ctx: ctx, cancel: cancel, listener: listener, core: instance,
		user: value.SocksUser, password: value.SocksPass,
		connections: make(map[*ownedConn]struct{}),
		accepted:    make(chan struct{}, maxAcceptedConnections), udpLinks: make(chan struct{}, maxUDPLinks),
		closeDone: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

func (s *socksServer) Dial(ctx context.Context, destination xnet.Destination) (net.Conn, error) {
	return s.core.Dial(ctx, destination)
}

func (s *socksServer) track(raw net.Conn, cancel context.CancelFunc) (*ownedConn, error) {
	c := &ownedConn{Conn: raw}
	c.onClose = func() {
		if cancel != nil {
			cancel()
		}
		s.mu.Lock()
		delete(s.connections, c)
		s.mu.Unlock()
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		_ = c.Close()
		return nil, errBoundary
	}
	s.connections[c] = struct{}{}
	s.mu.Unlock()
	return c, nil
}

func (s *socksServer) accept() {
	defer s.wg.Done()
	for {
		raw, err := s.listener.Accept()
		if err != nil {
			return
		}
		select {
		case s.accepted <- struct{}{}:
		default:
			_ = raw.Close()
			continue
		}
		conn, err := s.track(raw, nil)
		if err != nil {
			<-s.accepted
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.accepted }()
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

func (s *socksServer) authenticate(conn net.Conn) error {
	if conn.SetDeadline(time.Now().Add(nativeHandshakeTimeout)) != nil {
		return errBoundary
	}
	reader := varbin.StubReader(conn) // no buffering past the exact handshake
	greeting, err := socks5.ReadAuthRequest(reader)
	if err != nil {
		return errBoundary
	}
	if !bytes.Contains(greeting.Methods, []byte{socks5.AuthTypeUsernamePassword}) {
		_ = socks5.WriteAuthResponse(conn, socks5.AuthResponse{Method: socks5.AuthTypeNoAcceptedMethods})
		return errBoundary
	}
	if socks5.WriteAuthResponse(conn, socks5.AuthResponse{Method: socks5.AuthTypeUsernamePassword}) != nil {
		return errBoundary
	}
	auth, err := socks5.ReadUsernamePasswordAuthRequest(reader)
	if err != nil {
		return errBoundary
	}
	// Evaluate both comparisons, with no credential-bearing error/log path.
	valid := subtle.ConstantTimeCompare([]byte(auth.Username), []byte(s.user)) &
		subtle.ConstantTimeCompare([]byte(auth.Password), []byte(s.password))
	status := socks5.UsernamePasswordStatusFailure
	if valid == 1 {
		status = socks5.UsernamePasswordStatusSuccess
	}
	if socks5.WriteUsernamePasswordAuthResponse(conn, socks5.UsernamePasswordAuthResponse{Status: status}) != nil || valid != 1 {
		return errBoundary
	}
	return nil
}

func (s *socksServer) handle(conn *ownedConn) {
	if s.authenticate(conn) != nil {
		return
	}
	request, err := socks5.ReadRequest(varbin.StubReader(conn))
	if err != nil {
		return
	}
	if request.Command != socks5.CommandConnect {
		_ = socks5.WriteResponse(conn, socks5.Response{ReplyCode: socks5.ReplyCodeUnsupported})
		return // Never call the upstream handler that creates a UDP listener.
	}
	if request.Destination.Fqdn == uot.MagicAddress && request.Destination.Port == 0 {
		if socks5.WriteResponse(conn, socks5.Response{ReplyCode: socks5.ReplyCodeSuccess}) == nil {
			s.handleUOT(conn)
		}
		return
	}
	destination, err := tunnelDestination(request.Destination, false)
	if err != nil {
		_ = socks5.WriteResponse(conn, socks5.Response{ReplyCode: socks5.ReplyCodeAddressTypeUnsupported})
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	remote, err := s.dialTracked(ctx, destination)
	if err != nil {
		_ = socks5.WriteResponse(conn, socks5.Response{ReplyCode: socks5.ReplyCodeFailure})
		return
	}
	defer remote.Close()
	if socks5.WriteResponse(conn, socks5.Response{ReplyCode: socks5.ReplyCodeSuccess}) != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	// Closing either direction cancels the other; both goroutines are accounted
	// for by the server wait group and both connections are tracked for Stop.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_, _ = io.Copy(remote, conn)
		_ = remote.Close()
		_ = conn.Close()
	}()
	_, _ = io.Copy(conn, remote)
	_ = conn.Close()
}

func (s *socksServer) dialTracked(parent context.Context, destination xnet.Destination) (*ownedConn, error) {
	ctx, cancel := context.WithCancel(parent)
	timer := time.AfterFunc(nativeHandshakeTimeout, cancel)
	raw, err := s.core.Dial(ctx, destination)
	timer.Stop()
	if err != nil || raw == nil || ctx.Err() != nil {
		cancel()
		if raw != nil {
			_ = raw.Close()
		}
		return nil, errBoundary
	}
	return s.track(raw, cancel)
}

func tunnelDestination(value M.Socksaddr, udp bool) (xnet.Destination, error) {
	if value.Port == 0 || value.Fqdn == uot.MagicAddress || value.Fqdn == uot.LegacyMagicAddress {
		return xnet.Destination{}, errBoundary
	}
	var address xnet.Address
	if value.Fqdn != "" {
		if value.Addr.IsValid() || !validDNSName(strings.ToLower(value.Fqdn)) {
			return xnet.Destination{}, errBoundary
		}
		address = xnet.DomainAddress(value.Fqdn)
	} else {
		if !value.Addr.IsValid() || value.Addr.IsUnspecified() || value.Addr.IsMulticast() {
			return xnet.Destination{}, errBoundary
		}
		address = xnet.IPAddress(net.IP(value.Addr.AsSlice()))
	}
	if udp {
		return xnet.UDPDestination(address, xnet.Port(value.Port)), nil
	}
	return xnet.TCPDestination(address, xnet.Port(value.Port)), nil
}

func (s *socksServer) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.cancel()
		connections := make([]*ownedConn, 0, len(s.connections))
		for conn := range s.connections {
			connections = append(connections, conn)
		}
		s.mu.Unlock()
		_ = s.listener.Close()
		// Native socket/handler cleanup starts independently of core.Close, which
		// may block. The manager rejects future starts if this bounded close fails.
		go func() {
			var closeWG sync.WaitGroup
			for _, conn := range connections {
				closeWG.Add(1)
				go func(c *ownedConn) { defer closeWG.Done(); _ = c.Close() }(conn)
			}
			coreDone := make(chan error, 1)
			go func() { coreDone <- s.core.Close() }()
			nativeDone := make(chan struct{})
			go func() { closeWG.Wait(); s.wg.Wait(); close(nativeDone) }()
			timer := time.NewTimer(nativeStopTimeout)
			defer timer.Stop()
			for nativeDone != nil || coreDone != nil {
				select {
				case <-nativeDone:
					nativeDone = nil
				case err := <-coreDone:
					if err != nil {
						s.closeErr = errBoundary
					}
					coreDone = nil
				case <-timer.C:
					s.closeErr = errBoundary
					close(s.closeDone)
					return
				}
			}
			close(s.closeDone)
		}()
	})
	<-s.closeDone
	return s.closeErr
}
