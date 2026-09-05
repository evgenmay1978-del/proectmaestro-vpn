package main

import (
	"bytes"
	"context"
	"math"
	"net"
	"strconv"
	"sync"
	"time"

	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/log"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/app/router"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/json"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/transport/internet"
	_ "github.com/xtls/xray-core/transport/internet/splithttp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
)

const (
	statusOK = iota
	statusInvalidInput
	statusBusy
	statusStaleSession
	statusStartFailed
	statusJNIError
	statusStopFailed
)

type runningInstance interface {
	Dial(context.Context, xnet.Destination) (net.Conn, error)
	Close() error
}

type xrayInstance struct{ *core.Instance }

func (i *xrayInstance) Dial(ctx context.Context, destination xnet.Destination) (net.Conn, error) {
	// core.Dial supplies its own private instance context. Only exported session
	// APIs are used here; no fabricated internal context keys or direct fallback.
	ctx = session.ContextWithInbound(ctx, &session.Inbound{Tag: "maestro-cdn-socks"})
	ctx = session.SetForcedOutboundTagToContext(ctx, "maestro-cdn")
	return core.Dial(ctx, i.Instance, destination)
}

func closeBounded(instance runningInstance) error {
	if instance == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- instance.Close() }()
	timer := time.NewTimer(nativeStopTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errBoundary
	}
}

type engineManager struct {
	mu       sync.Mutex
	lastID   int64
	poisoned bool
	session  *engineSession
	dialer   protectedDialer
	start    func(context.Context, []byte) (runningInstance, error)
}

var liveEngine = &engineManager{start: startXray}

func init() {
	// The Xray global is set once, before any instance can dial. Per-session state
	// changes atomically inside our dialer; the upstream global is never swapped live.
	internet.UseAlternativeSystemDialer(&liveEngine.dialer)
}

func main() {}

func startXray(ctx context.Context, raw []byte) (runningInstance, error) {
	config, err := core.LoadConfig("json", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	instance, err := core.NewWithContext(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := instance.Start(); err != nil {
		// The manager owns all failed-start cleanup and records a close timeout
		// before it can permit another generation.
		return &xrayInstance{instance}, err
	}
	return &xrayInstance{instance}, nil
}

func (m *engineManager) Start(id int64, raw []byte, protect socketProtector) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 || id > math.MaxInt32 || protect == nil {
		return statusInvalidInput
	}
	if m.session != nil || m.poisoned {
		return statusBusy
	}
	if id <= m.lastID {
		return statusStaleSession
	}
	// Even failed attempts consume the generation, preventing a delayed callback
	// from ever being interpreted as a later attempt with the same session ID.
	m.lastID = id
	value, err := parseTransport(raw)
	if err != nil {
		return statusInvalidInput
	}
	config, err := value.config(id)
	if err != nil {
		return statusInvalidInput
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &engineSession{
		id: id, address: net.JoinHostPort(value.Address, strconv.Itoa(value.Port)),
		ctx: ctx, cancel: cancel, protect: protect, conns: make(map[*sessionConn]struct{}),
	}
	s.active.Store(true)
	m.dialer.current.Store(s)
	instance, err := m.start(ctx, config)
	if err != nil {
		s.deactivate()
		m.dialer.current.CompareAndSwap(s, nil)
		m.poisoned = closeBounded(instance) != nil
		return statusStartFailed
	}
	boundary, err := newSOCKSServer(ctx, value, instance)
	if err != nil {
		s.deactivate()
		m.dialer.current.CompareAndSwap(s, nil)
		m.poisoned = closeBounded(instance) != nil
		return statusStartFailed
	}
	s.core = boundary
	m.session = s
	return statusOK
}

func (m *engineManager) Stop(id int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.session
	if s == nil || s.id != id {
		return statusOK
	}
	s.deactivate()
	m.dialer.current.CompareAndSwap(s, nil)
	err := closeBounded(s.core)
	m.session = nil
	if err != nil {
		// Cleanup that timed out must never overlap a later account/engine.
		m.poisoned = true
		return statusStopFailed
	}
	return statusOK
}
