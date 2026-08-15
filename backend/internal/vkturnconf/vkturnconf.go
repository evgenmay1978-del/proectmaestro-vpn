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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

var allowedLogins = [...]string{"wapmix", "wapmixx", "wapmix2"}

// AllowedLogins returns a copy of the three owner-approved logins, so callers
// (the admin panel) can enumerate them without importing the private array.
func AllowedLogins() []string { return append([]string(nil), allowedLogins[:]...) }

const (
	DefaultWorkers     = 9
	DefaultFingerprint = "chrome"
	DefaultObfsMode    = "audio"
)

var DefaultClientIDs = []string{"6287487", "8202606"}

// Client contains the secrets unique to one MaestroVPN login.
type Client struct {
	Password string             `json:"password"`
	WG       subgen.VKTurnCreds `json:"wg"`
}

// ProbeStatus is the persisted, non-secret state of the provider-only room probe.
type ProbeStatus string

const (
	ProbeStatusActive   ProbeStatus = "active"
	ProbeStatusChecking ProbeStatus = "checking"
	ProbeStatusFailed   ProbeStatus = "failed"
	candidateProbeLease             = 5 * time.Minute
	disabledRollbackHash             = "DISABLED_PENDING_VERIFIED_ROOM"
)

var (
	// These sentinels let the HTTP boundary classify a candidate failure once,
	// without re-reading mutable state or exposing persistence details.
	ErrCandidateInvalid     = errors.New("vkturn: invalid candidate")
	ErrCandidateBusy        = errors.New("vkturn: candidate probe already running")
	ErrCandidatePersistence = errors.New("vkturn: candidate persistence failed")
	ErrCandidateStale       = errors.New("vkturn: candidate probe lease is stale")

	processStartedAt       = time.Now()
	probeGenerationCounter atomic.Uint64
)

// Config is the complete WDTT client configuration served by maestro-panel.
// Clients must contain exactly the three owner-approved logins.
type Config struct {
	Enabled               bool              `json:"enabled"`
	MinVersionCode        int               `json:"min_version_code"`
	Server                string            `json:"server"`
	VKHashes              []string          `json:"vk_hashes"`
	CandidateVKHashes     []string          `json:"candidate_vk_hashes,omitempty"`
	LastKnownGoodVKHashes []string          `json:"last_known_good_vk_hashes,omitempty"`
	ProbeStatus           ProbeStatus       `json:"probe_status,omitempty"`
	ProbeStartedAt        time.Time         `json:"probe_started_at,omitempty"`
	ProbeCheckedAt        time.Time         `json:"probe_checked_at,omitempty"`
	ProbeErrorCode        string            `json:"probe_error_code,omitempty"`
	Clients               map[string]Client `json:"clients"`
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
	// ProbeGeneration was briefly persisted by an unreleased Task 7 revision.
	// Accept that one known transitional field for forward recovery, but never
	// write it again: the Task 7 base decoder is strict and must remain a valid
	// rollback reader for every newly persisted state.
	var document struct {
		Config
		DeprecatedProbeGeneration uint64 `json:"probe_generation,omitempty"`
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode vkturn config: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("decode vkturn config: trailing JSON data")
	}
	cfg := document.Config
	cfg.decodeRollbackPlaceholder()
	cfg.normalizeProbeState()
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
	cp.CandidateVKHashes = append([]string(nil), c.CandidateVKHashes...)
	cp.LastKnownGoodVKHashes = append([]string(nil), c.LastKnownGoodVKHashes...)
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
	mu                    sync.RWMutex
	path                  string
	cfg                   *Config // nil = not configured / feature off
	leaseNow              func() time.Duration
	activeProbeGeneration uint64
	probeLeaseUntil       time.Duration
	beforePathLock         func() // deterministic transaction seam for package tests
	beforeCandidatePersist func() // deterministic check/write seam for package tests
}

func newStore(path string) *Store {
	return &Store{path: path, leaseNow: func() time.Duration { return time.Since(processStartedAt) }}
}

func nextProbeGeneration() uint64 {
	for {
		if generation := probeGenerationCounter.Add(1); generation != 0 {
			return generation
		}
	}
}

func (s *Store) leaseTimeLocked() time.Duration {
	if s.leaseNow == nil {
		return time.Since(processStartedAt)
	}
	return s.leaseNow()
}

func (s *Store) clearProbeLeaseLocked() {
	s.activeProbeGeneration = 0
	s.probeLeaseUntil = 0
}

func configLockPath(path string) string { return path + ".lock" }

func (s *Store) withPathLockLocked(fn func() error) (err error) {
	if s.beforePathLock != nil {
		s.beforePathLock()
	}
	if s.path == "" {
		return fn()
	}
	unlock, err := acquireConfigFileLock(configLockPath(s.path))
	if err != nil {
		return err
	}
	defer func() {
		if unlockErr := unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()
	return fn()
}

func (s *Store) refreshFromDiskLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && s.cfg == nil {
			return nil
		}
		return fmt.Errorf("read current vkturn config: %w", err)
	}
	disk, err := parse(b)
	if err != nil {
		return err
	}
	ownerMatches := s.cfg != nil && disk.ProbeStatus == ProbeStatusChecking &&
		s.cfg.ProbeStatus == ProbeStatusChecking &&
		disk.ProbeStartedAt.Equal(s.cfg.ProbeStartedAt) &&
		sameStrings(disk.CandidateVKHashes, s.cfg.CandidateVKHashes)
	if s.activeProbeGeneration != 0 && !ownerMatches {
		s.clearProbeLeaseLocked()
	}
	s.cfg = disk
	return nil
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
	s := newStore(path)
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
func NewInMemory() *Store { return newStore("") }

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
	cp := c.clone()
	cp.normalizeProbeState()
	if err := cp.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withPathLockLocked(func() error { return s.persistLocked(cp) })
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
	return s.withPathLockLocked(func() error {
		if err := s.refreshFromDiskLocked(); err != nil {
			return err
		}
		next := mutate(s.cfg.clone())
		if next == nil {
			return fmt.Errorf("vkturn: update produced no config")
		}
		next.normalizeProbeState()
		if err := next.Validate(); err != nil {
			return err
		}
		return s.persistLocked(next.clone())
	})
}

// StageCandidate acquires the single probe lease without changing VKHashes,
// which remains the active list served to every installed client.
func (s *Store) StageCandidate(hashes []string, startedAt time.Time) (uint64, error) {
	if err := ValidateProviderHashes(hashes); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrCandidateInvalid, err)
	}
	if startedAt.IsZero() {
		return 0, fmt.Errorf("%w: candidate start time is required", ErrCandidateInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var generation uint64
	err := s.withPathLockLocked(func() error {
		if err := s.refreshFromDiskLocked(); err != nil {
			return err
		}
		if s.cfg == nil {
			return fmt.Errorf("%w: config is not initialized", ErrCandidateInvalid)
		}
		leaseNow := s.leaseTimeLocked()
		if s.activeProbeGeneration != 0 && leaseNow < s.probeLeaseUntil {
			return ErrCandidateBusy
		}
		next := s.cfg.clone()
		next.CandidateVKHashes = append([]string(nil), hashes...)
		next.LastKnownGoodVKHashes = append([]string(nil), next.VKHashes...)
		next.ProbeStatus = ProbeStatusChecking
		probeStartedAt := startedAt.UTC()
		// ProbeStartedAt already belongs to the rollback-compatible schema. Make it
		// a strictly increasing persisted owner marker so a late callback from a
		// replaced Store/process cannot promote a newer candidate.
		if !next.ProbeStartedAt.IsZero() && !probeStartedAt.After(next.ProbeStartedAt) {
			probeStartedAt = next.ProbeStartedAt.Add(time.Nanosecond)
		}
		next.ProbeStartedAt = probeStartedAt
		next.ProbeCheckedAt = time.Time{}
		next.ProbeErrorCode = ""
		if err := next.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrCandidateInvalid, err)
		}
		generation = nextProbeGeneration()
		if err := s.persistLocked(next); err != nil {
			return err
		}
		s.activeProbeGeneration = generation
		s.probeLeaseUntil = leaseNow + candidateProbeLease
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrCandidateInvalid) || errors.Is(err, ErrCandidateBusy) {
			return 0, err
		}
		return 0, fmt.Errorf("%w: %v", ErrCandidatePersistence, err)
	}
	return generation, nil
}

// PromoteCandidate atomically makes the successfully probed candidate active.
func (s *Store) PromoteCandidate(generation uint64, checkedAt time.Time) error {
	if checkedAt.IsZero() {
		return fmt.Errorf("vkturn: candidate check time is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.withPathLockLocked(func() error {
		if err := s.refreshFromDiskLocked(); err != nil {
			return err
		}
		if s.cfg == nil || s.cfg.ProbeStatus != ProbeStatusChecking || len(s.cfg.CandidateVKHashes) == 0 ||
			generation == 0 || generation != s.activeProbeGeneration {
			return ErrCandidateStale
		}
		next := s.cfg.clone()
		next.VKHashes = append([]string(nil), next.CandidateVKHashes...)
		next.LastKnownGoodVKHashes = append([]string(nil), next.CandidateVKHashes...)
		next.CandidateVKHashes = nil
		next.ProbeStatus = ProbeStatusActive
		next.ProbeCheckedAt = checkedAt.UTC()
		next.ProbeErrorCode = ""
		if err := next.Validate(); err != nil {
			return err
		}
		if s.beforeCandidatePersist != nil {
			s.beforeCandidatePersist()
		}
		if err := s.persistLocked(next); err != nil {
			return err
		}
		s.clearProbeLeaseLocked()
		return nil
	})
	if errors.Is(err, ErrCandidateStale) {
		return err
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCandidatePersistence, err)
	}
	return nil
}

// RejectCandidate records only a fixed safe code and keeps the active/LKG list.
func (s *Store) RejectCandidate(generation uint64, code string, checkedAt time.Time) error {
	if !validProbeErrorCode(code) {
		return fmt.Errorf("vkturn: probe error code is invalid")
	}
	if checkedAt.IsZero() {
		return fmt.Errorf("vkturn: candidate check time is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.withPathLockLocked(func() error {
		if err := s.refreshFromDiskLocked(); err != nil {
			return err
		}
		if s.cfg == nil || s.cfg.ProbeStatus != ProbeStatusChecking ||
			generation == 0 || generation != s.activeProbeGeneration {
			return ErrCandidateStale
		}
		next := s.cfg.clone()
		next.CandidateVKHashes = nil
		next.LastKnownGoodVKHashes = append([]string(nil), next.VKHashes...)
		next.ProbeStatus = ProbeStatusFailed
		next.ProbeCheckedAt = checkedAt.UTC()
		next.ProbeErrorCode = code
		if err := next.Validate(); err != nil {
			return err
		}
		if s.beforeCandidatePersist != nil {
			s.beforeCandidatePersist()
		}
		if err := s.persistLocked(next); err != nil {
			return err
		}
		s.clearProbeLeaseLocked()
		return nil
	})
	if errors.Is(err, ErrCandidateStale) {
		return err
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCandidatePersistence, err)
	}
	return nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Store) persistLocked(cp *Config) error {
	if s.path != "" {
		committed, err := writeConfigFile(s.path, cp)
		if committed {
			s.cfg = cp
		}
		if err != nil {
			return err
		}
	}
	s.cfg = cp
	return nil
}

// writeConfigFile writes c to a fresh 0600 temp then atomically renames it over
// path. It creates a unique temp file in the target directory, forces mode 0600,
// fsyncs the file before rename and fsyncs the parent directory after rename.
func writeConfigFile(path string, c *Config) (bool, error) {
	disk := c.clone()
	disk.encodeRollbackPlaceholder()
	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return false, err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return false, err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return false, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, err
	}
	if err := syncConfigDirectory(dir); err != nil {
		return true, err
	}
	return true, nil
}

// Validate permits only the room list to be empty while disabled so a fresh
// installation can save the complete base settings before its first verified
// room is promoted. Enabling still requires an active room.
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
	if err := validateHashList("vk_hashes", c.VKHashes, c.Enabled); err != nil {
		return err
	}
	status := c.ProbeStatus
	if status == "" {
		status = ProbeStatusActive
	}
	switch status {
	case ProbeStatusActive:
		if len(c.CandidateVKHashes) != 0 || c.ProbeErrorCode != "" {
			return fmt.Errorf("active probe state contains candidate or error")
		}
	case ProbeStatusChecking:
		if err := validateHashList("candidate_vk_hashes", c.CandidateVKHashes, true); err != nil {
			return err
		}
		if c.ProbeStartedAt.IsZero() || !c.ProbeCheckedAt.IsZero() || c.ProbeErrorCode != "" {
			return fmt.Errorf("checking probe state is incomplete")
		}
	case ProbeStatusFailed:
		if len(c.CandidateVKHashes) != 0 || c.ProbeCheckedAt.IsZero() || !validProbeErrorCode(c.ProbeErrorCode) {
			return fmt.Errorf("failed probe state is incomplete")
		}
	default:
		return fmt.Errorf("probe_status is invalid")
	}
	if len(c.LastKnownGoodVKHashes) != 0 {
		if err := validateHashList("last_known_good_vk_hashes", c.LastKnownGoodVKHashes, true); err != nil {
			return err
		}
	}
	if len(c.Clients) != len(allowedLogins) {
		return fmt.Errorf("clients must contain exactly wapmix, wapmixx and wapmix2")
	}
	for _, login := range allowedLogins {
		client, ok := c.Clients[login]
		if !ok {
			return fmt.Errorf("client %q is required", login)
		}
		if err := validateClient(login, client); err != nil {
			return err
		}
	}
	return nil
}

// ValidateProviderHashes is the one provider-room validator shared by storage
// and the process runner. Values are normalized before this boundary.
func ValidateProviderHashes(hashes []string) error {
	return validateHashList("vk_hashes", hashes, true)
}

func validateHashList(field string, hashes []string, required bool) error {
	if (required && len(hashes) == 0) || len(hashes) > 4 {
		return fmt.Errorf("%s must contain 1..4 values", field)
	}
	seenHashes := make(map[string]struct{}, len(hashes))
	for i, hash := range hashes {
		hash = strings.TrimSpace(hash)
		if hash == "" || hash != hashes[i] {
			return fmt.Errorf("%s[%d] is empty", field, i)
		}
		if len(hash) < 8 || len(hash) > 160 || hash == disabledRollbackHash || strings.ContainsFunc(hash, func(r rune) bool {
			return !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-'
		}) {
			return fmt.Errorf("%s[%d] is not a provider room hash", field, i)
		}
		if _, exists := seenHashes[hash]; exists {
			return fmt.Errorf("%s[%d] is duplicated", field, i)
		}
		seenHashes[hash] = struct{}{}
	}
	return nil
}

func validProbeErrorCode(code string) bool {
	if len(code) < 1 || len(code) > 64 {
		return false
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func (c *Config) normalizeProbeState() {
	if c.ProbeStatus == "" {
		c.ProbeStatus = ProbeStatusActive
	}
	if len(c.LastKnownGoodVKHashes) == 0 {
		c.LastKnownGoodVKHashes = append([]string(nil), c.VKHashes...)
	}
}

func (c *Config) encodeRollbackPlaceholder() {
	if c != nil && !c.Enabled && len(c.VKHashes) == 0 {
		c.VKHashes = []string{disabledRollbackHash}
	}
}

func (c *Config) decodeRollbackPlaceholder() {
	if c == nil || c.Enabled {
		return
	}
	if len(c.VKHashes) == 1 && c.VKHashes[0] == disabledRollbackHash {
		c.VKHashes = nil
	}
	if len(c.LastKnownGoodVKHashes) == 1 && c.LastKnownGoodVKHashes[0] == disabledRollbackHash {
		c.LastKnownGoodVKHashes = nil
	}
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
