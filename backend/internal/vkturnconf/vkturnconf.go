// Package vkturnconf holds the private, per-login WDTT/VK TURN configuration.
// The configuration file lives outside the repository (root-owned, 0600). It is
// loaded at startup and is runtime-editable from the admin panel through the
// file-backed Store, which validates and persists every edit atomically. The
// fail-closed invariant is preserved: an EXISTING malformed file aborts startup,
// a rejected edit leaves the previous config serving, and an absent/unset file
// means the transport is simply OFF — it never advertises a half-configured
// transport to clients.
package vkturnconf

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// MaxClients caps the WDTT client list. The upstream wdtt-server derives one
// DTLS wrap key per password and its own tooling assumes at most 10 generated
// passwords (maxGeneratedPasswords in the pinned linux-server), so the panel
// refuses to provision beyond that rather than discover the limit in prod.
const MaxClients = 10

const (
	// DefaultWorkers=18 (2 VK-call groups, was 9=1) to aggregate more parallel TURN
	// relays for throughput. Trade-off: each group of 9 is one extra anonymous VK join
	// at cold start (captcha exposure) — 18 is the moderate step chosen over 27, kept
	// safe by the client self-warm. Client validateCreds accepts 9..108 in steps of 9.
	DefaultWorkers     = 18
	DefaultFingerprint = "chrome"
	// DefaultObfsMode="video" (was "audio"): audio disguises as a voice call whose
	// media the carrier may bitrate-shape; video disguises as a video call (higher
	// allowed bitrate). One RTP payload-type byte on the wire; reversible instantly.
	DefaultObfsMode = "video"
)

var DefaultClientIDs = []string{"6287487", "8202606"}

// Client contains the secrets unique to one MaestroVPN login.
type Client struct {
	Password string             `json:"password"`
	WG       subgen.VKTurnCreds `json:"wg"`
}

// Config is the complete WDTT client configuration served by maestro-panel.
// Clients must contain exactly the three owner-approved logins.
type Config struct {
	Enabled        bool              `json:"enabled"`
	MinVersionCode int               `json:"min_version_code"`
	Server         string            `json:"server"`
	VKHashes       []string          `json:"vk_hashes"`
	Clients        map[string]Client `json:"clients"`
}

// Open reads and validates path. An empty path means the feature was not
// configured and returns nil without touching the filesystem. Any non-empty
// path is explicit operator intent, so errors are returned to make startup fail
// closed rather than silently serving an incomplete configuration.
// Open reads+validates path once and returns the config (nil for an empty path).
// It is the one-shot loader retained for the startup-equivalent unit tests;
// production and the panel use the file-backed Store (OpenStore) instead.
func Open(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vkturn config: %w", err)
	}
	return parse(b)
}

// parse decodes+validates a config document, rejecting unknown/trailing fields
// so a malformed file fails closed rather than serving a half-configured transport.
func parse(b []byte) (*Config, error) {
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode vkturn config: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("decode vkturn config: trailing JSON data")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate vkturn config: %w", err)
	}
	return &cfg, nil
}

// clone returns a deep copy so a snapshot handed out by the Store can never be
// mutated by (or observe) a concurrent edit. Client is an all-value struct, so a
// map value-copy is deep; only the slice/map headers need explicit copying.
func (c *Config) clone() *Config {
	if c == nil {
		return nil
	}
	cp := *c
	cp.VKHashes = append([]string(nil), c.VKHashes...)
	if c.Clients != nil {
		cp.Clients = make(map[string]Client, len(c.Clients))
		for k, v := range c.Clients {
			cp.Clients[k] = v
		}
	}
	return &cp
}

// Store is a file-backed, concurrency-safe holder for the WDTT config, editable
// at runtime from the admin panel. Reads take an RLock and hand out a clone; Set
// validates and atomically persists (0600 tmp+rename) BEFORE swapping the live
// config, so a rejected edit leaves the previous config serving — the same
// fail-closed guarantee the immutable-file startup path already provided.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  *Config // nil = not configured / feature off
}

// OpenStore loads path and wraps it for runtime edits. An empty path means the
// feature is UNCONFIGURED — it returns (nil, nil) so the caller treats WDTT as OFF
// (the panel then refuses edits with "storage not configured"), matching the legacy
// Open("") behaviour. A configured-but-missing file is fine — the Store starts empty
// (OFF) and the first panel Save creates it. An EXISTING file must still
// parse+validate, so a corrupt file aborts startup exactly like Open did.
// Tests that need an editable store without a backing file use NewInMemory.
func OpenStore(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	s := &Store{path: path}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("read vkturn config: %w", err)
	}
	cfg, err := parse(b)
	if err != nil {
		return nil, err
	}
	s.cfg = cfg
	return s, nil
}

// NewInMemory returns a Store with no backing file: edits validate and swap in
// memory but do NOT persist. For TESTS only — production must use OpenStore with a
// real MAESTRO_VKTURN_FILE path so config survives restart and lands in the 0600 file.
func NewInMemory() *Store { return &Store{} }

// Get returns a snapshot (clone) of the live config, or nil when unconfigured.
func (s *Store) Get() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.clone()
}

// Set validates c, persists it atomically, then swaps it in as the live config.
// A validation or write failure leaves the previous config untouched (fail-closed).
func (s *Store) Set(c *Config) error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	cp := c.clone()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		if err := writeConfigFile(s.path, cp); err != nil {
			return err
		}
	}
	s.cfg = cp
	return nil
}

// Update applies mutate to a clone of the live config entirely under the write
// lock, then validates+persists+swaps the result — so a read-modify-write from the
// panel cannot lose a concurrent edit (the Get()...Set() pair was racy). mutate
// receives the current config (nil when unconfigured) and returns the desired next
// config; returning nil aborts with no change. A validation/write error leaves the
// previous config serving (fail-closed).
func (s *Store) Update(mutate func(cur *Config) *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := mutate(s.cfg.clone())
	if next == nil {
		return fmt.Errorf("vkturn: update produced no config")
	}
	if err := next.Validate(); err != nil {
		return err
	}
	cp := next.clone()
	if s.path != "" {
		if err := writeConfigFile(s.path, cp); err != nil {
			return err
		}
	}
	s.cfg = cp
	return nil
}

// writeConfigFile writes c to a fresh 0600 temp then atomically renames it over
// path. It creates the temp with O_EXCL (removing any stale leftover first) so the
// secrets file is ALWAYS mode 0600 and is never written through a pre-planted
// symlink — os.WriteFile would keep a pre-existing tmp's looser mode.
func writeConfigFile(path string, c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Validate rejects partial configurations even when Enabled is false. This
// makes flipping the operational switch safe and predictable.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	if c.MinVersionCode <= 0 {
		return fmt.Errorf("min_version_code must be positive")
	}
	if strings.TrimSpace(c.Server) == "" {
		return fmt.Errorf("server is required")
	}
	host, port, err := net.SplitHostPort(c.Server)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return fmt.Errorf("server must be host:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("server port is invalid")
	}
	if len(c.VKHashes) == 0 || len(c.VKHashes) > 4 {
		return fmt.Errorf("vk_hashes must contain 1..4 values")
	}
	seenHashes := make(map[string]struct{}, len(c.VKHashes))
	for i, hash := range c.VKHashes {
		hash = strings.TrimSpace(hash)
		if hash == "" || hash != c.VKHashes[i] {
			return fmt.Errorf("vk_hashes[%d] is empty", i)
		}
		if len(hash) > 160 || strings.ContainsFunc(hash, func(r rune) bool {
			return !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("._~-", r)
		}) {
			return fmt.Errorf("vk_hashes[%d] contains unsafe characters", i)
		}
		if _, exists := seenHashes[hash]; exists {
			return fmt.Errorf("vk_hashes[%d] is duplicated", i)
		}
		seenHashes[hash] = struct{}{}
	}
	if len(c.Clients) > MaxClients {
		return fmt.Errorf("clients must contain at most %d logins", MaxClients)
	}
	for login, client := range c.Clients {
		if !validLogin(login) {
			return fmt.Errorf("client login %q is invalid", login)
		}
		if err := validateClient(login, client); err != nil {
			return err
		}
	}
	return nil
}

// validLogin bounds a WDTT client login: the same shape the customer store
// accepts (short, no spaces, no control characters). An empty clients map is
// valid — it just means nobody is served the transport.
func validLogin(login string) bool {
	if login == "" || len(login) > 64 || strings.TrimSpace(login) != login {
		return false
	}
	for _, r := range login {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("._-", r) {
			return false
		}
	}
	return true
}

// ClientFor returns a copy of the per-login configuration. Unknown logins fail
// closed; matching is intentionally case-sensitive.
func (c *Config) ClientFor(login string) (Client, bool) {
	if c == nil || !c.Enabled {
		return Client{}, false
	}
	client, ok := c.Clients[login]
	return client, ok
}

func validateClient(login string, client Client) error {
	password := strings.TrimSpace(client.Password)
	if password != client.Password || len(password) < 8 || len(password) > 128 {
		return fmt.Errorf("client %q password must be 8..128 characters", login)
	}
	for _, ch := range password {
		if ch < 0x21 || ch > 0x7e {
			return fmt.Errorf("client %q password contains unsafe characters", login)
		}
	}
	wg := client.WG
	if !validWGKey(wg.PeerPublicKey) || !validWGKey(wg.PrivateKey) {
		return fmt.Errorf("client %q wg keys must be 32-byte base64 values", login)
	}
	ip, network, err := net.ParseCIDR(wg.LocalAddress)
	if err != nil || ip.String() != strings.SplitN(wg.LocalAddress, "/", 2)[0] {
		return fmt.Errorf("client %q wg local_address is invalid", login)
	}
	ones, bits := network.Mask.Size()
	if ones != bits {
		return fmt.Errorf("client %q wg local_address must be a host prefix", login)
	}
	return nil
}

func validWGKey(s string) bool {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	return err == nil && len(b) == 32
}
