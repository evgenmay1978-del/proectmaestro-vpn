package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/varbin"
	"github.com/sagernet/sing/protocol/socks/socks5"
	xnet "github.com/xtls/xray-core/common/net"
)

type boundaryFixture struct {
	dials        atomic.Int32
	destinations chan xnet.Destination
	closed       chan struct{}
	blockClose   chan struct{}
}

func (f *boundaryFixture) Dial(ctx context.Context, destination xnet.Destination) (net.Conn, error) {
	f.dials.Add(1)
	f.destinations <- destination
	local, remote := net.Pipe()
	go func() {
		defer remote.Close()
		stop := context.AfterFunc(ctx, func() { _ = remote.Close() })
		defer stop()
		packet := make([]byte, maxUDPPayload+1)
		for {
			n, err := remote.Read(packet)
			if err != nil {
				return
			}
			if _, err := remote.Write(packet[:n]); err != nil {
				return
			}
		}
	}()
	return local, nil
}

func (f *boundaryFixture) Close() error {
	if f.blockClose != nil {
		<-f.blockClose
	}
	close(f.closed)
	return nil
}

func availablePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func newBoundaryFixture(t *testing.T) (*socksServer, *boundaryFixture, transport) {
	t.Helper()
	value := fixtureTransport()
	value.SocksPort = availablePort(t)
	fixture := &boundaryFixture{destinations: make(chan xnet.Destination, 128), closed: make(chan struct{})}
	server, err := newSOCKSServer(context.Background(), value, fixture)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if server.Close() != nil {
			t.Error("boundary close failed")
		}
	})
	return server, fixture, value
}

func boundaryClient(t *testing.T, value transport) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(value.SocksPort)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	return conn
}

func authenticateClient(t *testing.T, conn net.Conn, user, password string) byte {
	t.Helper()
	if socks5.WriteAuthRequest(conn, socks5.AuthRequest{Methods: []byte{socks5.AuthTypeUsernamePassword}}) != nil {
		t.Fatal("greeting write")
	}
	response, err := socks5.ReadAuthResponse(varbin.StubReader(conn))
	if err != nil || response.Method != socks5.AuthTypeUsernamePassword {
		t.Fatal("authentication not required")
	}
	if socks5.WriteUsernamePasswordAuthRequest(conn, socks5.UsernamePasswordAuthRequest{Username: user, Password: password}) != nil {
		t.Fatal("auth write")
	}
	auth, err := socks5.ReadUsernamePasswordAuthResponse(varbin.StubReader(conn))
	if err != nil {
		t.Fatal("auth response")
	}
	return auth.Status
}

func connectClient(t *testing.T, conn net.Conn, command byte, destination M.Socksaddr) byte {
	t.Helper()
	if socks5.WriteRequest(conn, socks5.Request{Command: command, Destination: destination}) != nil {
		t.Fatal("request write")
	}
	response, err := socks5.ReadResponse(varbin.StubReader(conn))
	if err != nil {
		t.Fatal("request response")
	}
	return response.ReplyCode
}

func TestSOCKSRequiresAuthenticationAndRejectsUDPAssociate(t *testing.T) {
	_, fixture, value := newBoundaryFixture(t)
	conn := boundaryClient(t, value)
	if socks5.WriteAuthRequest(conn, socks5.AuthRequest{Methods: []byte{socks5.AuthTypeNotRequired}}) != nil {
		t.Fatal("greeting")
	}
	response, err := socks5.ReadAuthResponse(varbin.StubReader(conn))
	if err != nil || response.Method != socks5.AuthTypeNoAcceptedMethods {
		t.Fatal("accepted unauthenticated client")
	}
	wrong := boundaryClient(t, value)
	if authenticateClient(t, wrong, value.SocksUser, value.SocksPass+"x") != socks5.UsernamePasswordStatusFailure {
		t.Fatal("accepted wrong password")
	}
	auth := boundaryClient(t, value)
	if authenticateClient(t, auth, value.SocksUser, value.SocksPass) != 0 {
		t.Fatal("valid auth")
	}
	if connectClient(t, auth, socks5.CommandUDPAssociate, M.ParseSocksaddr("127.0.0.1:12345")) != socks5.ReplyCodeUnsupported {
		t.Fatal("UDP association accepted")
	}
	if fixture.dials.Load() != 0 {
		t.Fatal("unauthorized request spent transport traffic")
	}
	// The only bound native socket is TCP. There is no UDP port even after auth.
	udp, err := net.ListenPacket("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(value.SocksPort)))
	if err != nil {
		t.Fatal("native UDP listener exists")
	}
	_ = udp.Close()
}

func TestSOCKSConnectForwardsDomainThroughCoreAndStopClosesClient(t *testing.T) {
	server, fixture, value := newBoundaryFixture(t)
	conn := boundaryClient(t, value)
	if authenticateClient(t, conn, value.SocksUser, value.SocksPass) != 0 {
		t.Fatal("auth")
	}
	if connectClient(t, conn, socks5.CommandConnect, M.Socksaddr{Fqdn: "never-resolve.invalid", Port: 443}) != 0 {
		t.Fatal("connect")
	}
	want := []byte("through-vless-only")
	if _, err := conn.Write(want); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, want) {
		t.Fatal("TCP payload mismatch")
	}
	if destination := <-fixture.destinations; destination.Network != xnet.Network_TCP || destination.Address.Domain() != "never-resolve.invalid" {
		t.Fatal("destination changed or resolved locally")
	}
	if server.Close() != nil {
		t.Fatal("close")
	}
	if _, err := conn.Read(got); err == nil {
		t.Fatal("client survived stop")
	}
}

func TestNativeCloseDoesNotWaitForBlockedCoreBeforeClosingSockets(t *testing.T) {
	value := fixtureTransport()
	value.SocksPort = availablePort(t)
	fixture := &boundaryFixture{destinations: make(chan xnet.Destination, 1), closed: make(chan struct{}), blockClose: make(chan struct{})}
	server, err := newSOCKSServer(context.Background(), value, fixture)
	if err != nil {
		t.Fatal(err)
	}
	conn := boundaryClient(t, value)
	if authenticateClient(t, conn, value.SocksUser, value.SocksPass) != 0 {
		t.Fatal("auth")
	}
	done := make(chan error, 1)
	go func() { done <- server.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("accepted client survived core stall")
	}
	select {
	case err := <-done:
		if !errors.Is(err, errBoundary) {
			t.Fatal("blocked close reported success")
		}
	case <-time.After(nativeStopTimeout + time.Second):
		t.Fatal("native close unbounded")
	}
	close(fixture.blockClose)
	<-fixture.closed
}
