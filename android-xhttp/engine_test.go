package main

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

type fakeInstance struct{ closed atomic.Int32 }

func (i *fakeInstance) Close() error { i.closed.Add(1); return nil }
func (i *fakeInstance) Dial(context.Context, xnet.Destination) (net.Conn, error) {
	return nil, errors.New("fixture refuses network")
}

func TestEngineLifecycleAndFailedStartCleanup(t *testing.T) {
	instance := &fakeInstance{}
	fail := false
	manager := &engineManager{
		start: func(context.Context, []byte) (runningInstance, error) {
			if fail {
				return instance, errors.New("private fixture detail must not cross ABI")
			}
			return instance, nil
		},
	}
	value := fixtureTransport()
	value.SocksPort = availablePort(t)
	raw := fixtureJSON(t, value)
	protect := func(int64, int) bool { return false }
	if manager.Start(1, raw, protect) != statusOK {
		t.Fatal("start")
	}
	if manager.Start(2, raw, protect) != statusBusy {
		t.Fatal("second engine accepted")
	}
	if manager.Stop(2) != statusOK || instance.closed.Load() != 0 {
		t.Fatal("stale stop changed current engine")
	}
	if manager.Stop(1) != statusOK || manager.Stop(1) != statusOK || instance.closed.Load() != 1 {
		t.Fatal("stop is not idempotent")
	}
	if manager.Start(1, raw, protect) != statusStaleSession {
		t.Fatal("session ID reused")
	}
	fail = true
	if manager.Start(2, raw, protect) != statusStartFailed || instance.closed.Load() != 2 ||
		manager.session != nil || manager.dialer.current.Load() != nil {
		t.Fatal("failed start left a live engine/session")
	}
	if manager.Start(2, raw, protect) != statusStaleSession {
		t.Fatal("failed generation reused")
	}
}

func TestRealXrayAuthenticatedSOCKSStartStop(t *testing.T) {
	// Local boundary only: no proxy request is sent, so no CDN or external DNS
	// traffic is generated. Android JNI/protect and two-Go-runtime coexistence
	// are separate device gates.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	value := fixtureTransport()
	value.SocksPort, _ = strconv.Atoi(port)
	manager := &engineManager{start: startXray}
	internet.UseAlternativeSystemDialer(&manager.dialer)
	defer internet.UseAlternativeSystemDialer(&liveEngine.dialer)
	if manager.Start(1, fixtureJSON(t, value), func(int64, int) bool { return false }) != statusOK {
		t.Fatal("real Xray start failed")
	}
	defer manager.Stop(1)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err = conn.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2)
	if _, err = io.ReadFull(conn, response); err != nil || response[0] != 5 || response[1] != 2 {
		t.Fatal("real SOCKS listener did not require username/password")
	}
	if manager.Stop(1) != statusOK {
		t.Fatal("real Xray stop failed")
	}
	if extra, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second); err == nil {
		_ = extra.Close()
		t.Fatal("listener survived stop")
	}
}

func TestFailedSOCKSBindClosesStartedCoreAndRevokesSession(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	value := fixtureTransport()
	value.SocksPort = listener.Addr().(*net.TCPAddr).Port
	instance := &fakeInstance{}
	manager := &engineManager{start: func(context.Context, []byte) (runningInstance, error) {
		return instance, nil
	}}
	defer manager.Stop(2)
	raw := fixtureJSON(t, value)
	protect := func(int64, int) bool { return false }
	if manager.Start(1, raw, protect) != statusStartFailed || instance.closed.Load() != 1 ||
		manager.session != nil || manager.dialer.current.Load() != nil || manager.poisoned {
		t.Fatal("failed listener bind left a core/session or hid successful cleanup")
	}
	if manager.Start(1, raw, protect) != statusStaleSession {
		t.Fatal("failed generation reused")
	}
	_ = listener.Close()
	if manager.Start(2, raw, protect) != statusOK || manager.Stop(2) != statusOK || instance.closed.Load() != 2 {
		t.Fatal("cleaned-up bind failure prevented a later valid generation")
	}
}
