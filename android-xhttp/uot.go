package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"sync"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"
)

// Pinned Xray XUDP PacketWriter requires payload+666 <= buf.Size (8192).
// Reject instead of triggering its silent oversize drop. This also keeps every
// core.Dial UDP Write in one Xray buffer/datagram.
const maxUDPPayload = 8192 - 666

type uotAssociation struct {
	server  *socksServer
	conn    *ownedConn
	ctx     context.Context
	cancel  context.CancelFunc
	request uot.Request
	links   map[M.Socksaddr]*ownedConn // used only by the request reader
	writeMu sync.Mutex
}

func (s *socksServer) handleUOT(conn *ownedConn) {
	request, err := uot.ReadRequest(conn)
	if err != nil {
		return
	}
	if request.IsConnect {
		if _, err := tunnelDestination(request.Destination, true); err != nil {
			return
		}
	}
	ctx, cancel := context.WithCancel(s.ctx)
	a := &uotAssociation{server: s, conn: conn, ctx: ctx, cancel: cancel, request: *request, links: make(map[M.Socksaddr]*ownedConn)}
	defer func() {
		cancel()
		_ = conn.Close()
		for _, link := range a.links {
			_ = link.Close()
			<-s.udpLinks
		}
	}()
	_ = conn.SetDeadline(time.Time{})
	for {
		_ = conn.SetReadDeadline(time.Now().Add(nativeIdleTimeout))
		destination, packet, err := readUOTPacket(conn, *request)
		if err != nil {
			return
		}
		link := a.links[destination]
		if link == nil {
			select {
			case s.udpLinks <- struct{}{}:
			default:
				return
			}
			target, err := tunnelDestination(destination, true)
			if err != nil {
				<-s.udpLinks
				return
			}
			link, err = s.dialTracked(ctx, target)
			if err != nil {
				<-s.udpLinks
				return
			}
			a.links[destination] = link
			s.wg.Add(1)
			go a.readResponses(destination, link)
		}
		// The virtual Xray connection does not implement deadlines. Cancel and
		// close it on a bounded write timeout instead of relying on SetDeadline.
		timer := time.AfterFunc(nativeWriteTimeout, func() { cancel(); _ = conn.Close(); _ = link.Close() })
		n, err := link.Write(packet)
		timer.Stop()
		if err != nil || n != len(packet) || ctx.Err() != nil {
			return
		}
	}
}

func (a *uotAssociation) readResponses(destination M.Socksaddr, link *ownedConn) {
	defer a.server.wg.Done()
	defer a.cancel()
	defer a.conn.Close()
	defer link.Close()
	packet := make([]byte, maxUDPPayload+1)
	for {
		n, err := link.Read(packet)
		if err != nil || n == 0 || n > maxUDPPayload || a.ctx.Err() != nil {
			return
		}
		a.writeMu.Lock()
		_ = a.conn.SetWriteDeadline(time.Now().Add(nativeWriteTimeout))
		// Receiving traffic also renews the association's idle read deadline.
		_ = a.conn.SetReadDeadline(time.Now().Add(nativeIdleTimeout))
		err = writeUOTPacket(a.conn, a.request, destination, packet[:n])
		a.writeMu.Unlock()
		if err != nil {
			return
		}
	}
}

func readUOTPacket(reader io.Reader, request uot.Request) (M.Socksaddr, []byte, error) {
	destination := request.Destination
	var err error
	if !request.IsConnect {
		destination, err = uot.AddrParser.ReadAddrPort(reader)
		if err != nil {
			return M.Socksaddr{}, nil, errBoundary
		}
	}
	if _, err = tunnelDestination(destination, true); err != nil {
		return M.Socksaddr{}, nil, errBoundary
	}
	var size uint16
	if binary.Read(reader, binary.BigEndian, &size) != nil || size == 0 || size > maxUDPPayload {
		return M.Socksaddr{}, nil, errBoundary
	}
	packet := make([]byte, int(size))
	if _, err := io.ReadFull(reader, packet); err != nil {
		return M.Socksaddr{}, nil, errBoundary
	}
	return destination, packet, nil
}

func writeUOTPacket(writer io.Writer, request uot.Request, destination M.Socksaddr, packet []byte) error {
	if len(packet) == 0 || len(packet) > maxUDPPayload {
		return errBoundary
	}
	var frame bytes.Buffer
	if !request.IsConnect {
		if uot.AddrParser.WriteAddrPort(&frame, destination) != nil {
			return errBoundary
		}
	}
	if binary.Write(&frame, binary.BigEndian, uint16(len(packet))) != nil {
		return errBoundary
	}
	_, _ = frame.Write(packet)
	for frame.Len() > 0 {
		n, err := writer.Write(frame.Bytes())
		if err != nil || n <= 0 || n > frame.Len() {
			return errBoundary
		}
		frame.Next(n)
	}
	return nil
}
