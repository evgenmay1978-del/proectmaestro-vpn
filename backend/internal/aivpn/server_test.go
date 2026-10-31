package aivpn

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubRest mimics the parts of aivpn-server's management API the panel uses.
type stubRest struct {
	mu       sync.Mutex
	clients  []clientView
	key      string
	listHits int
	postHits int
	keyHits  int

	// keyByName, when set, gives each client its own key (by client name), so a
	// test can prove two devices receive two different credentials. key stays
	// the default for names without an entry.
	keyByName map[string]string
	// created records the client names the panel asked to create, in order.
	created []string

	listStatus int
	postStatus int
	keyStatus  int
}

func (s *stubRest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/clients":
		s.listHits++
		if s.listStatus != 0 {
			w.WriteHeader(s.listStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(s.clients)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/clients":
		s.postHits++
		if s.postStatus != 0 {
			w.WriteHeader(s.postStatus)
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		view := clientView{ID: "id-" + in.Name, Name: in.Name, Enabled: true}
		s.clients = append(s.clients, view)
		s.created = append(s.created, in.Name)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(view)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/connection-key"):
		s.keyHits++
		if s.keyStatus != 0 {
			w.WriteHeader(s.keyStatus)
			return
		}
		// The key belongs to the client the id names: resolve id -> name so a
		// per-device stub can answer with that device's own key.
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/clients/"), "/connection-key")
		key := s.key
		for _, c := range s.clients {
			if c.ID == id {
				if k, ok := s.keyByName[c.Name]; ok {
					key = k
				}
				break
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"connection_key": key})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *stubRest) hits() (list, post, key int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listHits, s.postHits, s.keyHits
}

// createdNames returns the client names the panel asked to create, in order.
func (s *stubRest) createdNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.created...)
}

// startStub serves h on a unix socket in a short temp path: unix socket paths
// are limited to about 100 bytes, so t.TempDir() is not short enough to trust.
func startStub(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "aivpnstub")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

func newTestServer(t *testing.T, cfg ServerConfig) *Server {
	t.Helper()
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func restConfig(sock string) ServerConfig {
	return ServerConfig{
		Enabled:        true,
		Logins:         []string{"alice", "bob"},
		MinVersionCode: 108,
		SocketPath:     sock,
		Timeout:        2 * time.Second,
		CacheTTL:       time.Minute,
	}
}

func TestServerRESTCreatesThenCaches(t *testing.T) {
	stub := &stubRest{key: testKey(t)}
	s := newTestServer(t, restConfig(startStub(t, stub)))
	ctx := context.Background()

	got, err := s.Key(ctx, "alice")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if got != stub.key {
		t.Fatalf("Key returned %q, want the server key", got)
	}
	if _, post, key := stub.hits(); post != 1 || key != 1 {
		t.Fatalf("first call: post=%d key=%d, want 1/1", post, key)
	}
	// Cached: no second management round trip.
	if _, err := s.Key(ctx, "alice"); err != nil {
		t.Fatalf("cached Key: %v", err)
	}
	if _, post, key := stub.hits(); post != 1 || key != 1 {
		t.Fatalf("cached call: post=%d key=%d, want 1/1", post, key)
	}
	// Invalidate (revocation) forces a refetch but not a new client.
	s.Invalidate("alice")
	if _, err := s.Key(ctx, "alice"); err != nil {
		t.Fatalf("Key after Invalidate: %v", err)
	}
	if _, post, key := stub.hits(); post != 1 || key != 2 {
		t.Fatalf("after invalidate: post=%d key=%d, want 1/2", post, key)
	}
}

func TestServerRESTCacheExpires(t *testing.T) {
	stub := &stubRest{key: testKey(t)}
	s := newTestServer(t, restConfig(startStub(t, stub)))
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }

	if _, err := s.Key(context.Background(), "alice"); err != nil {
		t.Fatalf("Key: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := s.Key(context.Background(), "alice"); err != nil {
		t.Fatalf("Key after TTL: %v", err)
	}
	if _, _, key := stub.hits(); key != 2 {
		t.Fatalf("key hits = %d, want 2 (cache must expire)", key)
	}
}

func TestServerRESTUsesExistingClient(t *testing.T) {
	stub := &stubRest{key: testKey(t), clients: []clientView{{ID: "abc", Name: "alice", Enabled: true}}}
	s := newTestServer(t, restConfig(startStub(t, stub)))
	if _, err := s.Key(context.Background(), "alice"); err != nil {
		t.Fatalf("Key: %v", err)
	}
	if _, post, _ := stub.hits(); post != 0 {
		t.Fatalf("post hits = %d, want 0 for an existing client", post)
	}
}

func TestServerRESTRevokedClientIsNotServed(t *testing.T) {
	// Revoked on the server (enabled=false). The panel must not resurrect it: the
	// create call answers 409 and no key may be handed out.
	stub := &stubRest{
		key:        testKey(t),
		clients:    []clientView{{ID: "abc", Name: "alice", Enabled: false}},
		postStatus: http.StatusConflict,
	}
	s := newTestServer(t, restConfig(startStub(t, stub)))
	if _, err := s.Key(context.Background(), "alice"); err == nil {
		t.Fatal("expected an error for a revoked client")
	}
	if _, _, key := stub.hits(); key != 0 {
		t.Fatalf("connection-key hits = %d, want 0", key)
	}
}

func TestServerRESTRejectsMalformedKey(t *testing.T) {
	stub := &stubRest{key: "aivpn://not-a-real-key"}
	s := newTestServer(t, restConfig(startStub(t, stub)))
	if _, err := s.Key(context.Background(), "alice"); err == nil {
		t.Fatal("expected an error for an unusable key")
	}
	// Must not be cached: the next request asks again.
	if _, err := s.Key(context.Background(), "alice"); err == nil {
		t.Fatal("expected an error on retry too")
	}
	if _, _, key := stub.hits(); key != 2 {
		t.Fatalf("key hits = %d, want 2 (bad keys must not be cached)", key)
	}
}

func TestServerRESTWhitelistAndErrors(t *testing.T) {
	s := newTestServer(t, restConfig(startStub(t, &stubRest{key: testKey(t)})))
	if _, err := s.Key(context.Background(), "mallory"); !errors.Is(err, ErrNoClient) {
		t.Fatalf("non-whitelisted login: err = %v, want ErrNoClient", err)
	}

	stub := &stubRest{key: testKey(t), listStatus: http.StatusInternalServerError}
	s = newTestServer(t, restConfig(startStub(t, stub)))
	if _, err := s.Key(context.Background(), "alice"); err == nil {
		t.Fatal("expected an error when the management API fails")
	}

	stub = &stubRest{
		key:       testKey(t),
		clients:   []clientView{{ID: "abc", Name: "alice", Enabled: true}},
		keyStatus: http.StatusInternalServerError,
	}
	s = newTestServer(t, restConfig(startStub(t, stub)))
	if _, err := s.Key(context.Background(), "alice"); err == nil {
		t.Fatal("expected an error when the key endpoint fails")
	}
}

func TestServerDisabled(t *testing.T) {
	if _, err := NewServer(ServerConfig{Enabled: false}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("NewServer(disabled) err = %v, want ErrDisabled", err)
	}
	var s *Server
	if s.Enabled() {
		t.Fatal("nil Server must report disabled")
	}
	if _, err := (&Server{}).Key(context.Background(), "alice"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled Server.Key err = %v, want ErrDisabled", err)
	}
}

func TestNewServerValidation(t *testing.T) {
	base := restConfig("/tmp/x.sock")
	cases := map[string]ServerConfig{
		"no min vc":       {Enabled: true, Logins: []string{"alice"}, SocketPath: "/tmp/x.sock"},
		"no logins":       {Enabled: true, MinVersionCode: 108, SocketPath: "/tmp/x.sock"},
		"bad login":       {Enabled: true, MinVersionCode: 108, Logins: []string{"bad login"}, SocketPath: "/tmp/x.sock"},
		"no transport":    {Enabled: true, MinVersionCode: 108, Logins: []string{"alice"}},
		"both transports": {Enabled: true, MinVersionCode: 108, Logins: []string{"alice"}, SocketPath: "/tmp/x.sock", CLIPath: "/bin/true", CLIConfigPath: "/etc/aivpn/server.json", ServerAddr: testServer},
		"cli without cfg": {Enabled: true, MinVersionCode: 108, Logins: []string{"alice"}, CLIPath: "/bin/true", ServerAddr: testServer},
		"cli bad addr":    {Enabled: true, MinVersionCode: 108, Logins: []string{"alice"}, CLIPath: "/bin/true", CLIConfigPath: "/etc/aivpn/server.json", ServerAddr: "no-port"},
	}
	for name, cfg := range cases {
		if _, err := NewServer(cfg); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
	if _, err := NewServer(base); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// writeStubCLI writes a fake aivpn-server script that prints key inside a banner
// (as the real binary does). addExit is the --add-client exit status: the real
// binary fails when the client already exists.
func writeStubCLI(t *testing.T, key string, addExit int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	path := filepath.Join(t.TempDir(), "aivpn-server")
	exit := "0"
	if addExit != 0 {
		exit = "1"
	}
	script := strings.Join([]string{
		"#!/bin/sh",
		"case $3 in",
		"--add-client) echo exists >&2; exit " + exit + " ;;",
		"esac",
		"echo Connection-Key",
		"echo " + key,
		"exit 0",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub CLI: %v", err)
	}
	return path
}

func cliConfig(cli string) ServerConfig {
	return ServerConfig{
		Enabled:        true,
		Logins:         []string{"alice"},
		MinVersionCode: 108,
		CLIPath:        cli,
		CLIConfigPath:  "/etc/aivpn/server.json",
		ServerAddr:     testServer,
		Timeout:        5 * time.Second,
	}
}

func TestServerCLIIssuesKey(t *testing.T) {
	key := testKey(t)
	s := newTestServer(t, cliConfig(writeStubCLI(t, key, 1)))
	got, err := s.Key(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if got != key {
		t.Fatalf("CLI key mismatch")
	}
}

func TestServerCLIErrorDoesNotLeakKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	key := testKey(t)
	path := filepath.Join(t.TempDir(), "aivpn-server")
	script := "#!/bin/sh\necho " + key + "\nexit 3\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub CLI: %v", err)
	}
	s := newTestServer(t, cliConfig(path))
	_, err := s.Key(context.Background(), "alice")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), strings.TrimPrefix(key, Prefix)) {
		t.Fatalf("error leaks the connection key: %s", err)
	}
	if !strings.Contains(err.Error(), "aivpn://[redacted]") {
		t.Fatalf("error should show a redacted key, got: %s", err)
	}
}
