package main

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/xtls/xray-core/transport/internet"
)

type fakeInstance struct{ closed int }

func (i *fakeInstance) Close() error { i.closed++; return nil }

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
	raw := fixtureJSON(t, fixtureTransport())
	protect := func(int64, int) bool { return false }
	if manager.Start(1, raw, protect) != statusOK {
		t.Fatal("start")
	}
	if manager.Start(2, raw, protect) != statusBusy {
		t.Fatal("second engine accepted")
	}
	if manager.Stop(2) != statusOK || instance.closed != 0 {
		t.Fatal("stale stop changed current engine")
	}
	if manager.Stop(1) != statusOK || manager.Stop(1) != statusOK || instance.closed != 1 {
		t.Fatal("stop is not idempotent")
	}
	if manager.Start(1, raw, protect) != statusStaleSession {
		t.Fatal("session ID reused")
	}
	fail = true
	if manager.Start(2, raw, protect) != statusStartFailed || instance.closed != 2 ||
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
