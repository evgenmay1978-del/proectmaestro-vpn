package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
	"github.com/sagernet/sing/protocol/socks/socks5"
	xnet "github.com/xtls/xray-core/common/net"
)

func openUOTClient(t *testing.T, value transport, request uot.Request) (*uot.Conn, net.Conn) {
	t.Helper()
	raw := boundaryClient(t, value)
	if authenticateClient(t, raw, value.SocksUser, value.SocksPass) != 0 {
		t.Fatal("auth")
	}
	if connectClient(t, raw, socks5.CommandConnect, uot.RequestDestination(2)) != 0 {
		t.Fatal("UOT connect")
	}
	if uot.WriteRequest(raw, request) != nil {
		t.Fatal("UOT request")
	}
	return uot.NewConn(raw, request), raw
}

func TestUOTV2PreservesEachDatagramDestinationWithoutDNS(t *testing.T) {
	_, fixture, value := newBoundaryFixture(t)
	request := uot.Request{Destination: M.ParseSocksaddr("0.0.0.0:0")}
	client, _ := openUOTClient(t, value, request)
	for _, destination := range []M.Socksaddr{
		{Fqdn: "never-resolve.invalid", Port: 53},
		M.ParseSocksaddr("1.1.1.1:443"),
		M.ParseSocksaddr("[2606:4700:4700::1111]:53"),
	} {
		payload := []byte("packet-for-" + destination.String())
		packet := buf.As(payload)
		if client.WritePacket(packet, destination) != nil {
			t.Fatal("UOT packet write")
		}
		response := buf.NewSize(maxUDPPayload)
		gotDestination, err := client.ReadPacket(response)
		if err != nil || gotDestination != destination || !bytes.Equal(response.Bytes(), payload) {
			response.Release()
			t.Fatal("datagram destination/payload changed")
		}
		response.Release()
		got := <-fixture.destinations
		if got.Network != xnet.Network_UDP || int(got.Port) != int(destination.Port) {
			t.Fatal("core destination mismatch")
		}
		if destination.Fqdn != "" && got.Address.Domain() != destination.Fqdn {
			t.Fatal("FQDN was resolved locally")
		}
	}
	if fixture.dials.Load() != 3 {
		t.Fatal("destinations shared one UDP dispatcher")
	}
}

func TestUOTConnectedModeAndExplicitPayloadBounds(t *testing.T) {
	_, _, value := newBoundaryFixture(t)
	destination := M.ParseSocksaddr("1.1.1.1:53")
	request := uot.Request{IsConnect: true, Destination: destination}
	client, _ := openUOTClient(t, value, request)
	payload := bytes.Repeat([]byte{7}, maxUDPPayload)
	if client.WritePacket(buf.As(payload), destination) != nil {
		t.Fatal("maximum packet rejected")
	}
	response := buf.NewSize(maxUDPPayload)
	defer response.Release()
	got, err := client.ReadPacket(response)
	if err != nil || got != destination || !bytes.Equal(response.Bytes(), payload) {
		t.Fatal("connected packet truncated")
	}
	for _, length := range []int{0, maxUDPPayload + 1, 65535} {
		var frame bytes.Buffer
		_ = binary.Write(&frame, binary.BigEndian, uint16(length))
		if _, _, err := readUOTPacket(&frame, request); err == nil {
			t.Fatal("invalid size accepted")
		}
		if writeUOTPacket(&bytes.Buffer{}, request, destination, make([]byte, length)) == nil {
			t.Fatal("invalid response size accepted")
		}
	}
}

func TestUOTOversizeClosesAssociationBeforeCoreDispatch(t *testing.T) {
	_, fixture, value := newBoundaryFixture(t)
	request := uot.Request{IsConnect: true, Destination: M.ParseSocksaddr("1.1.1.1:53")}
	_, raw := openUOTClient(t, value, request)
	if binary.Write(raw, binary.BigEndian, uint16(maxUDPPayload+1)) != nil {
		t.Fatal("size write")
	}
	if _, err := raw.Read(make([]byte, 1)); err == nil {
		t.Fatal("oversize association survived")
	}
	if fixture.dials.Load() != 0 {
		t.Fatal("oversize packet reached core")
	}
}

func TestUOTStopClosesAssociationAndLinks(t *testing.T) {
	server, _, value := newBoundaryFixture(t)
	destination := M.ParseSocksaddr("1.1.1.1:53")
	client, raw := openUOTClient(t, value, uot.Request{IsConnect: true, Destination: destination})
	if client.WritePacket(buf.As([]byte("pending")), destination) != nil {
		t.Fatal("packet write")
	}
	response := buf.NewSize(maxUDPPayload)
	defer response.Release()
	if _, err := client.ReadPacket(response); err != nil {
		t.Fatal("packet read")
	}
	if server.Close() != nil {
		t.Fatal("stop did not join native handlers")
	}
	_ = raw.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := raw.Read(make([]byte, 1)); err == nil {
		t.Fatal("association survived stop")
	}
	server.mu.Lock()
	remaining := len(server.connections)
	server.mu.Unlock()
	if remaining != 0 || len(server.udpLinks) != 0 {
		t.Fatal("native links leaked")
	}
}
