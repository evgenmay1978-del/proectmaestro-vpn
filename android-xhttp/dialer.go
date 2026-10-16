package main

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

var errProtectedDial = errors.New("protected_dial_denied")

type socketProtector func(sessionID int64, fd int) bool

type engineSession struct {
	id      int64
	address string
	ctx     context.Context
	cancel  context.CancelFunc
	protect socketProtector
	active  atomic.Bool
	mu      sync.Mutex
	conns   map[*sessionConn]struct{}
	core    runningInstance
}

func (s *engineSession) deactivate() {
	s.active.Store(false)
	s.cancel()
	s.mu.Lock()
	conns := make([]*sessionConn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

type sessionConn struct {
	net.Conn
	session *engineSession
	once    sync.Once
	err     error
}

func (c *sessionConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.session.mu.Lock()
		delete(c.session.conns, c)
		c.session.mu.Unlock()
	})
	return c.err
}

type protectedDialer struct {
	current atomic.Pointer[engineSession]
}

func (d *protectedDialer) valid(s *engineSession) bool {
	return s != nil && d.current.Load() == s && s.active.Load() && s.ctx.Err() == nil
}

func (d *protectedDialer) Dial(ctx context.Context, src xnet.Address, dest xnet.Destination, opt *internet.SocketConfig) (net.Conn, error) {
	s := d.current.Load()
	if !d.valid(s) || s.protect == nil || opt == nil || int64(opt.Mark) != s.id ||
		dest.Network != xnet.Network_TCP || dest.Address == nil ||
		dest.Address.Family().IsDomain() || dest.NetAddr() != s.address ||
		(src != nil && src != xnet.AnyIP) {
		return nil, errProtectedDial
	}
	dialCtx, cancel := context.WithTimeout(ctx, 16*time.Second)
	stopCancel := context.AfterFunc(s.ctx, cancel)
	defer cancel()
	defer stopCancel()
	dialer := net.Dialer{
		Control: func(network, address string, raw syscall.RawConn) error {
			if !d.valid(s) || dialCtx.Err() != nil || address != s.address {
				return errProtectedDial
			}
			allowed := false
			err := raw.Control(func(fd uintptr) {
				// The callback must protect AND bind this original socket before
				// returning true. A panic/Java exception is a denial, never a log-only event.
				allowed = callProtector(s.protect, s.id, int(fd))
			})
			if err != nil || !allowed || !d.valid(s) || dialCtx.Err() != nil {
				return errProtectedDial
			}
			return nil
		},
	}
	conn, err := dialer.DialContext(dialCtx, "tcp", s.address)
	if err != nil {
		return nil, errProtectedDial
	}
	// Stop closes all accepted sockets, including one that completes concurrently
	// with cancellation. A late old-session result never joins the new session.
	s.mu.Lock()
	if !d.valid(s) {
		s.mu.Unlock()
		_ = conn.Close()
		return nil, errProtectedDial
	}
	tracked := &sessionConn{Conn: conn, session: s}
	s.conns[tracked] = struct{}{}
	s.mu.Unlock()
	return tracked, nil
}

func (d *protectedDialer) DestIpAddress() xnet.IP { return nil }

func callProtector(protect socketProtector, id int64, fd int) (allowed bool) {
	defer func() {
		if recover() != nil {
			allowed = false
		}
	}()
	return protect(id, fd)
}
