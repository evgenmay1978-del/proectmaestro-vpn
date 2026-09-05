package main

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

func localDialFixture(t *testing.T, protect socketProtector) (*protectedDialer, *engineSession, *net.TCPListener, xnet.Destination) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	s := &engineSession{
		id: 1, address: listener.Addr().String(), ctx: ctx, cancel: cancel,
		protect: protect, conns: make(map[*sessionConn]struct{}),
	}
	s.active.Store(true)
	d := &protectedDialer{}
	d.current.Store(s)
	t.Cleanup(s.deactivate)
	_, port, _ := net.SplitHostPort(s.address)
	p, _ := strconv.Atoi(port)
	dest := xnet.TCPDestination(xnet.IPAddress(net.ParseIP("127.0.0.1")), xnet.Port(p))
	return d, s, listener, dest
}

func assertNoAccept(t *testing.T, listener *net.TCPListener) {
	t.Helper()
	_ = listener.SetDeadline(time.Now().Add(40 * time.Millisecond))
	conn, err := listener.Accept()
	if err == nil {
		_ = conn.Close()
		t.Fatal("connection reached listener despite denial")
	}
}

func TestProtectedDialDeniesBeforeConnect(t *testing.T) {
	for _, mode := range []string{"false", "panic", "missing", "session_stopped", "wrong_cookie", "wrong_destination", "udp"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			protect := func(id int64, fd int) bool {
				calls.Add(1)
				if mode == "panic" {
					panic("fixture")
				}
				return false
			}
			if mode == "missing" {
				protect = nil
			}
			d, s, listener, dest := localDialFixture(t, protect)
			if mode == "session_stopped" {
				s.protect = func(int64, int) bool { s.deactivate(); return true }
			}
			opt := &internet.SocketConfig{Mark: 1}
			if mode == "wrong_cookie" {
				opt.Mark = 2
			}
			if mode == "wrong_destination" {
				dest.Port++
			}
			if mode == "udp" {
				dest.Network = xnet.Network_UDP
			}
			conn, err := d.Dial(context.Background(), nil, dest, opt)
			if err == nil || conn != nil {
				t.Fatal("dial did not fail closed")
			}
			assertNoAccept(t, listener)
		})
	}
}

func TestProtectedDialConnectsAndStopClosesRawSocket(t *testing.T) {
	var calls atomic.Int32
	d, s, listener, dest := localDialFixture(t, func(id int64, fd int) bool {
		calls.Add(1)
		return id == 1 && fd >= 0
	})
	conn, err := d.Dial(context.Background(), nil, dest, &internet.SocketConfig{Mark: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = listener.SetDeadline(time.Now().Add(time.Second))
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if calls.Load() != 1 {
		t.Fatal("socket protection was not called exactly once")
	}
	s.deactivate()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("stop left upstream socket open")
	}
}

func TestOldCachedTransportCannotUseNewProtector(t *testing.T) {
	var calls atomic.Int32
	d, s, listener, dest := localDialFixture(t, func(int64, int) bool {
		calls.Add(1)
		return true
	})
	s.id = 2
	if _, err := d.Dial(context.Background(), nil, dest, &internet.SocketConfig{Mark: 1}); err == nil {
		t.Fatal("old XHTTP cookie used new session")
	}
	if calls.Load() != 0 {
		t.Fatal("old transport reached new protector")
	}
	assertNoAccept(t, listener)
}
