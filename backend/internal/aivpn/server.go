package aivpn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ServerConfig configures the S1-backed source.
//
// DRAFT: this path talks to the LIVE AIVPN server when enabled. It is exercised
// in tests only against a local stub unix socket or a stub script — never
// against S1. SocketPath and CLIPath are mutually exclusive; the REST API is
// preferred because it needs no shell and returns the key as JSON.
type ServerConfig struct {
	Enabled        bool
	Logins         []string
	MinVersionCode int

	SocketPath    string // management REST over this unix socket
	CLIPath       string // aivpn-server binary (CLI fallback)
	CLIConfigPath string // CLI --config
	ServerAddr    string // CLI --server-ip (public host:port stamped into keys)

	Timeout  time.Duration // per management call (default 10s)
	CacheTTL time.Duration // how long an issued key is reused (default 10m)
}

// Server issues and fetches AIVPN client credentials from the AIVPN server on
// S1: client <login> is created on first request, then its connection key is read
// back and cached. Client names are logins, so the panel's own whitelist is the
// authority on who may have a client at all.
type Server struct {
	cfg      ServerConfig
	allowed  map[string]bool
	client   *http.Client // non-nil in REST mode
	sockPath string
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]cachedKey
}

type cachedKey struct {
	key     string
	expires time.Time
}

var _ Source = (*Server)(nil)

// NewServer validates cfg and builds the source. Both modes require a non-empty
// logins whitelist and MinVersionCode > 0: an AIVPN key is a live credential, so
// "everyone with a subscription token" is not an acceptable default.
func NewServer(cfg ServerConfig) (*Server, error) {
	if !cfg.Enabled {
		return nil, ErrDisabled
	}
	if cfg.MinVersionCode <= 0 {
		return nil, fmt.Errorf("aivpn: server source needs %s > 0", EnvMinVC)
	}
	if len(cfg.Logins) == 0 {
		return nil, fmt.Errorf("aivpn: server source needs %s", EnvLogins)
	}
	allowed := make(map[string]bool, len(cfg.Logins))
	for _, login := range cfg.Logins {
		login = strings.TrimSpace(login)
		if !validLogin(login) {
			return nil, fmt.Errorf("aivpn: %s: login %q must match [A-Za-z0-9._@-]{1,64}", EnvLogins, login)
		}
		allowed[login] = true
	}
	cfg.SocketPath = strings.TrimSpace(cfg.SocketPath)
	cfg.CLIPath = strings.TrimSpace(cfg.CLIPath)
	cfg.ServerAddr = strings.TrimSpace(cfg.ServerAddr)
	rest, cli := cfg.SocketPath != "", cfg.CLIPath != ""
	switch {
	case rest && cli:
		return nil, errors.New("aivpn: set either a unix socket or a CLI binary, not both")
	case !rest && !cli:
		return nil, errors.New("aivpn: server source needs a unix socket or a CLI binary")
	}
	if cli {
		if cfg.CLIConfigPath == "" {
			return nil, errors.New("aivpn: CLI source needs " + EnvCLIConfig)
		}
		if cfg.ServerAddr == "" {
			return nil, errors.New("aivpn: CLI source needs " + EnvServerAddr)
		}
		if err := validateHostPort(cfg.ServerAddr); err != nil {
			return nil, fmt.Errorf("aivpn: %s: %w", EnvServerAddr, err)
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 10 * time.Minute
	}
	s := &Server{cfg: cfg, allowed: allowed, sockPath: cfg.SocketPath, now: time.Now, cache: map[string]cachedKey{}}
	if rest {
		s.client = unixClient(cfg.SocketPath, cfg.Timeout)
	}
	return s, nil
}

// Enabled implements Source.
func (s *Server) Enabled() bool { return s != nil && s.cfg.Enabled }

// MinVersionCode implements Source.
func (s *Server) MinVersionCode() int {
	if s == nil {
		return 0
	}
	return s.cfg.MinVersionCode
}

// Allowed implements Source.
func (s *Server) Allowed(login string) bool { return s != nil && s.allowed[login] }

// Key implements Source: cached key, else create/fetch on the AIVPN server.
func (s *Server) Key(ctx context.Context, login string) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	if !s.Allowed(login) {
		return "", ErrNoClient
	}
	if key, ok := s.cached(login); ok {
		return key, nil
	}
	var (
		key string
		err error
	)
	if s.client != nil {
		key, err = s.keyHTTP(ctx, login)
	} else {
		key, err = s.keyCLI(ctx, login)
	}
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(key)
	if _, perr := Parse(key); perr != nil {
		// A malformed key is a server-side fault: never cache or serve it.
		return "", fmt.Errorf("aivpn: AIVPN server issued an unusable key: %w", perr)
	}
	s.remember(login, key)
	return key, nil
}

// Invalidate drops a cached key (revocation, or a client reset on the server).
func (s *Server) Invalidate(login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, login)
}

func (s *Server) cached(login string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[login]
	if !ok || !s.now().Before(e.expires) {
		return "", false
	}
	return e.key, true
}

func (s *Server) remember(login, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[login] = cachedKey{key: key, expires: s.now().Add(s.cfg.CacheTTL)}
}

// Management REST (unix socket). The Host part of the URL is irrelevant: the
// transport always dials the unix socket.
const mgmtBase = "http://aivpn"

type clientView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type statusError struct{ code int }

func (e *statusError) Error() string {
	return fmt.Sprintf("aivpn: management API returned HTTP %d", e.code)
}

var errAlreadyExists = errors.New("aivpn: client already exists")

func (s *Server) keyHTTP(ctx context.Context, login string) (string, error) {
	id, err := s.clientID(ctx, login)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mgmtBase+"/api/v1/clients/"+url.PathEscape(id)+"/connection-key", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		ConnectionKey string `json:"connection_key"`
	}
	if err := s.doJSON(req, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ConnectionKey) == "" {
		return "", errors.New("aivpn: management API returned an empty connection key")
	}
	return out.ConnectionKey, nil
}

// clientID returns the AIVPN client id for login, creating the client on first
// use. A client that exists but is disabled (revoked on the server) is treated
// as absent: revocation must win over the panel's whitelist.
func (s *Server) clientID(ctx context.Context, login string) (string, error) {
	clients, err := s.listClients(ctx)
	if err != nil {
		return "", err
	}
	if id, ok := pickClient(clients, login); ok {
		return id, nil
	}
	if err := s.createClient(ctx, login); err != nil && !errors.Is(err, errAlreadyExists) {
		return "", err
	}
	clients, err = s.listClients(ctx)
	if err != nil {
		return "", err
	}
	if id, ok := pickClient(clients, login); ok {
		return id, nil
	}
	return "", fmt.Errorf("aivpn: client %q is not visible on the AIVPN server after creation", login)
}

func (s *Server) listClients(ctx context.Context) ([]clientView, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mgmtBase+"/api/v1/clients", nil)
	if err != nil {
		return nil, err
	}
	var out []clientView
	if err := s.doJSON(req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) createClient(ctx context.Context, login string) error {
	body, err := json.Marshal(map[string]string{"name": login})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mgmtBase+"/api/v1/clients", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	err = s.doJSON(req, nil)
	var se *statusError
	if errors.As(err, &se) && se.code == http.StatusConflict {
		return errAlreadyExists
	}
	return err
}

// pickClient returns the id of the enabled client named login. A disabled
// (revoked) client yields no id: the caller then tries to create, the server
// answers 409, and the login stays keyless — which is the intended outcome of a
// revocation.
func pickClient(clients []clientView, login string) (string, bool) {
	for _, c := range clients {
		if c.Name != login {
			continue
		}
		if c.Enabled && c.ID != "" {
			return c.ID, true
		}
		return "", false
	}
	return "", false
}

func (s *Server) doJSON(req *http.Request, out any) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("aivpn: management API request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return &statusError{code: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("aivpn: management API returned invalid JSON: %w", err)
	}
	return nil
}

// unixClient returns an http.Client whose every request goes to sock.
func unixClient(sock string, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", sock)
		},
		DisableCompression: true,
		MaxIdleConns:       2,
		IdleConnTimeout:    30 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// CLI fallback. The binary prints the key inside a banner, so it has to be
// scraped; runCLI sanitizes everything before it can reach a log line.
var connKeyRe = regexp.MustCompile("aivpn://[A-Za-z0-9_-]+")

func (s *Server) keyCLI(ctx context.Context, login string) (string, error) {
	// --add-client fails when the name already exists; that is expected on every
	// request after the first, so its error is deliberately ignored. The
	// --show-client output is what matters.
	_, _ = s.runCLI(ctx, "--add-client", login)
	out, err := s.runCLI(ctx, "--show-client", login, "--server-ip", s.cfg.ServerAddr)
	if err != nil {
		return "", err
	}
	key := connKeyRe.FindString(string(out))
	if key == "" {
		return "", errors.New("aivpn: aivpn-server CLI printed no connection key")
	}
	return key, nil
}

func (s *Server) runCLI(ctx context.Context, args ...string) ([]byte, error) {
	argv := append([]string{"--config", s.cfg.CLIConfigPath}, args...)
	cmd := exec.CommandContext(ctx, s.cfg.CLIPath, argv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("aivpn: %s %s failed: %w (%s)", filepath.Base(s.cfg.CLIPath), args[0], err, sanitizeCLIOutput(out))
	}
	return out, nil
}

// sanitizeCLIOutput redacts connection keys and collapses the banner into one
// short line: CLI output must never carry key material into an error string.
func sanitizeCLIOutput(out []byte) string {
	s := connKeyRe.ReplaceAllString(string(out), "aivpn://[redacted]")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
