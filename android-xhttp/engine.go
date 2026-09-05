package main

import (
	"bytes"
	"context"
	"math"
	"net"
	"strconv"
	"sync"

	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/log"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/json"
	_ "github.com/xtls/xray-core/proxy/socks"
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
	Close() error
}

type engineManager struct {
	mu      sync.Mutex
	lastID  int64
	session *engineSession
	dialer  protectedDialer
	start   func(context.Context, []byte) (runningInstance, error)
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
		_ = instance.Close()
		return nil, err
	}
	return instance, nil
}

func (m *engineManager) Start(id int64, raw []byte, protect socketProtector) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 || id > math.MaxInt32 || protect == nil {
		return statusInvalidInput
	}
	if m.session != nil {
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
		if instance != nil {
			_ = instance.Close()
		}
		return statusStartFailed
	}
	s.core = instance
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
	err := s.core.Close()
	m.session = nil
	if err != nil {
		return statusStopFailed
	}
	return statusOK
}
