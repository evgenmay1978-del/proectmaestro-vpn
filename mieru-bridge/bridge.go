// Package mierubridge embeds the mieru proxy client in-process for the
// MaestroVPN Android app.
//
// It replaces the previously-bundled `libmieru.so` child process. Instead of
// forking the mieru CLI (`mieru run`), the app calls Start(), which brings up
// the exact same proxy multiplexer + local socks5 server that the CLI would,
// but inside the app's own process. Benefits:
//
//   - Immune to the Android 12+ phantom-process killer, which on aggressive
//     OEM ROMs reaps exec'd child processes a few minutes after screen-off and
//     silently broke mieru in the background.
//   - No second process to supervise, no idle binary burning battery.
//   - gomobile produces real Android .so for arm64 AND armv7, so 32-bit boxes
//     get mieru natively (superseding the static linux_armv7 binary hack).
//
// The wiring is a faithful copy of mieru's own pkg/cli/client.go runClient in
// its "mobile app" mode (RPC port 0): build the client mux from the active
// profile, point a local socks5 server at it, serve on 127.0.0.1:<socks5Port>.
// The carrier connection to the mita server is made with mieru's own net.Dial,
// OUTSIDE sing-box's outbound machinery, so it is never protect()'d — just as the
// exec'd child's socket wasn't. Under the tun's strict_route that unprotected
// socket would loop back into the local mieru socks outbound, so the backend pins
// the mita IP to a DIRECT route (subgen mieruDirectRule). That rule is STILL
// required in-process; behaviour is identical to the child-process model.
package mierubridge

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/enfein/mieru/v3/pkg/appctl/appctlcommon"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"github.com/enfein/mieru/v3/pkg/log"
	"github.com/enfein/mieru/v3/pkg/protocol"
	"github.com/enfein/mieru/v3/pkg/sockopts"
	"github.com/enfein/mieru/v3/pkg/socks5"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	mu       sync.Mutex
	server   *socks5.Server
	listener *net.TCPListener
	mux      *protocol.Mux
)

func init() {
	// The app owns the lifecycle; silence mieru's logger so it never tries to
	// open a log file in the working directory (matches apis/client).
	log.SetFormatter(&log.NilFormatter{})
}

// Start parses a mieru client config JSON — the same document the old helper
// wrote for `mieru run` — and brings up a local socks5 server on
// 127.0.0.1:<socks5Port>, in-process. It returns once the listener is bound
// (so callers can start sing-box immediately afterwards), or an error. Calling
// Start while already running is a no-op and returns nil.
func Start(configJSON string) error {
	mu.Lock()
	defer mu.Unlock()
	if server != nil {
		return nil
	}

	var cfg appctlpb.ClientConfig
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("parse mieru config: %w", err)
	}

	// Resolve the active profile by name.
	active := cfg.GetActiveProfile()
	var profile *appctlpb.ClientProfile
	for _, p := range cfg.GetProfiles() {
		if p.GetProfileName() == active {
			profile = p
			break
		}
	}
	if profile == nil {
		return fmt.Errorf("active profile %q not found in config", active)
	}
	if err := appctlcommon.ValidateClientConfigSingleProfile(profile); err != nil {
		return fmt.Errorf("invalid mieru profile: %w", err)
	}

	port := int(cfg.GetSocks5Port())
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid socks5 port %d", port)
	}

	// A real resolver is fine: server endpoints are IP literals, so DNS is only
	// a fallback. nil would disable resolution entirely.
	resolver := &net.Resolver{}
	m, err := appctlcommon.NewClientMuxFromProfile(profile, nil, nil, resolver, nil)
	if err != nil {
		return fmt.Errorf("build mieru mux: %w", err)
	}

	scfg := &socks5.Config{
		UseProxy:         true,
		AuthOpts:         socks5.Auth{ClientSideAuthentication: true},
		ProxyDialer:      m, // *protocol.Mux satisfies socks5.ProxyDialer
		Resolver:         resolver,
		HandshakeTimeout: 10 * time.Second,
	}
	if profile.GetHandshakeMode() == appctlpb.HandshakeMode_HANDSHAKE_NO_WAIT {
		scfg.HandshakeNoWait = true
	}
	srv, err := socks5.New(scfg)
	if err != nil {
		_ = m.Close()
		return fmt.Errorf("create socks5 server: %w", err)
	}

	// Loopback only — sing-box dials 127.0.0.1:<port>; never expose on LAN.
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		_ = m.Close()
		return fmt.Errorf("listen 127.0.0.1:%d: %w", port, err)
	}
	_ = sockopts.ApplyTCPControl(l, sockopts.DefaultListenerControl())

	mux = m
	server = srv
	listener = l
	go func(s *socks5.Server, ln *net.TCPListener) {
		// Recover the ACCEPT loop: mieru shares libbox's Go runtime, so an unrecovered
		// panic here would kill the whole app (tun + all 4 protocols). This degrades it
		// to "mieru down, the other 3 survive". NOTE it does NOT cover mieru's per-conn
		// (socks5.ServeConn) or mux goroutines — mieru spawns those itself and we can't
		// wrap them, so those panics remain process-fatal. That residual risk is exactly
		// why MieruHelper keeps the exec'd-binary fallback as the real isolation tier.
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("mieru bridge: socks5 serve panic: %v", r)
				go Stop()
			}
		}()
		_ = s.Serve(ln) // returns when Close() is called
	}(srv, l)
	return nil
}

// Stop tears down the local socks5 server and the proxy mux. Safe to call when
// not running.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if server != nil {
		_ = server.Close()
		server = nil
	}
	if listener != nil {
		_ = listener.Close()
		listener = nil
	}
	if mux != nil {
		_ = mux.Close()
		mux = nil
	}
}

// IsRunning reports whether the in-process socks5 server is currently up.
func IsRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return server != nil
}
