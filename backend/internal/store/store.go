// Package store is the customer registry for maestro-panel: who is provisioned,
// until when, and the per-protocol credentials needed to render their sing-box
// subscription. Backed by a single JSON file with an atomic write — plenty for a
// panel with up to a few thousand customers, and trivially testable.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// ErrNotFound is returned when no customer matches.
var ErrNotFound = errors.New("store: customer not found")

// Customer is one subscriber. SubToken is the opaque id in the subscription URL
// (/sub/<SubToken>); Login is what the customer types once in the app.
type Customer struct {
	Login    string              `json:"login"`
	SubToken string              `json:"sub_token"`
	Expires  time.Time           `json:"expires"`
	Disabled bool                `json:"disabled"`
	VLESS    *subgen.VLESSCreds  `json:"vless,omitempty"`
	Hy2      *subgen.Hy2Creds    `json:"hy2,omitempty"`
	Naive    *subgen.NaiveCreds  `json:"naive,omitempty"`
	AnyTLS   *subgen.AnyTLSCreds `json:"anytls,omitempty"`
	VLESS3   *subgen.VLESSCreds  `json:"vless3,omitempty"` // VLESS-Reality on the 3rd node (S3)
	WG       *subgen.WGCreds     `json:"wg,omitempty"`     // AmneziaWG (S3); ⛔ nil for ALL real customers until the with_awg libbox is the fleet engine
	// olcRTC is NOT per-customer: its room/key are GLOBAL (one srv, one room) and live in
	// package olcconf (the panel's olcrtc.json). WHO gets olcRTC is the MAESTRO_OLC_LOGINS
	// allowlist gate in package api. So there is no OLC field here.
	// Devices is the set of distinct app installs that have activated/polled this
	// account (deviceId → first-seen). It backs the per-account device cap, enforced
	// at the subscription chokepoint (/sub, /claim) so it covers ALL four protocols at
	// once. Only the app sends a device id; other clients (Karing, the bot :2096 sub)
	// are absent here and never counted — they are capped natively by 3x-ui limitIp.
	Devices map[string]time.Time `json:"devices,omitempty"`
}

// Active reports whether the subscription is usable right now.
func (c *Customer) Active() bool {
	return !c.Disabled && time.Now().Before(c.Expires)
}

// ToSubgen maps a customer to the subscription generator input.
func (c *Customer) ToSubgen() subgen.Customer {
	return subgen.Customer{Name: c.Login, VLESS: c.VLESS, Hy2: c.Hy2, Naive: c.Naive, AnyTLS: c.AnyTLS, VLESS3: c.VLESS3, WG: c.WG}
}

// clone returns an independent deep copy so callers can read it without the lock
// while another goroutine mutates the stored record in place (e.g. Extend writes
// Expires under the write lock). Accessors clone UNDER their read lock, so the
// returned value is a consistent snapshot — no data race.
func (c *Customer) clone() *Customer {
	if c == nil {
		return nil
	}
	cp := *c
	if c.VLESS != nil {
		v := *c.VLESS
		cp.VLESS = &v
	}
	if c.Hy2 != nil {
		h := *c.Hy2
		cp.Hy2 = &h
	}
	if c.Naive != nil {
		n := *c.Naive
		cp.Naive = &n
	}
	if c.AnyTLS != nil {
		a := *c.AnyTLS
		cp.AnyTLS = &a
	}
	if c.VLESS3 != nil {
		v := *c.VLESS3
		cp.VLESS3 = &v
	}
	if c.WG != nil {
		w := *c.WG
		cp.WG = &w
	}
	if c.Devices != nil {
		d := make(map[string]time.Time, len(c.Devices))
		for k, v := range c.Devices {
			d[k] = v
		}
		cp.Devices = d
	}
	return &cp
}

// Store is a JSON-file-backed customer registry, safe for concurrent use.
type Store struct {
	path  string
	mu    sync.RWMutex
	byTok map[string]*Customer
	byLog map[string]*Customer
}

// Open loads (or creates) the store at path.
func Open(path string) (*Store, error) {
	s := &Store{path: path, byTok: map[string]*Customer{}, byLog: map[string]*Customer{}}
	data, err := os.ReadFile(path) //#nosec G304 -- path is the server-configured store file (cfg/env), never request-derived
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", path, err)
	}
	var list []*Customer
	if len(data) > 0 {
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("store: parse %s: %w", path, err)
		}
	}
	for _, c := range list {
		s.byTok[c.SubToken] = c
		s.byLog[c.Login] = c
	}
	return s, nil
}

// flush writes the whole store atomically. Caller holds the write lock.
func (s *Store) flush() error {
	list := make([]*Customer, 0, len(s.byLog))
	for _, c := range s.byLog {
		list = append(list, c)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("store: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("store: rename: %w", err)
	}
	return nil
}

// Put inserts or replaces a customer (keyed by Login) and persists.
func (s *Store) Put(c *Customer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.byLog[c.Login]; ok && old.SubToken != c.SubToken {
		delete(s.byTok, old.SubToken)
	}
	s.byLog[c.Login] = c
	s.byTok[c.SubToken] = c
	return s.flush()
}

// ByToken returns the customer for a subscription token.
func (s *Store) ByToken(tok string) (*Customer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c, ok := s.byTok[tok]; ok {
		return c.clone(), nil
	}
	return nil, ErrNotFound
}

// ByLogin returns the customer for a login.
func (s *Store) ByLogin(login string) (*Customer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c, ok := s.byLog[login]; ok {
		return c.clone(), nil
	}
	return nil, ErrNotFound
}

// List returns a snapshot of all customers.
func (s *Store) List() []*Customer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Customer, 0, len(s.byLog))
	for _, c := range s.byLog {
		out = append(out, c.clone())
	}
	return out
}

// Extend pushes a customer's expiry by d (from max(now, current expiry)).
func (s *Store) Extend(login string, d time.Duration) (*Customer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byLog[login]
	if !ok {
		return nil, ErrNotFound
	}
	base := time.Now()
	if c.Expires.After(base) {
		base = c.Expires
	}
	c.Expires = base.Add(d)
	c.Disabled = false
	return c.clone(), s.flush()
}

// TouchDeviceByToken records deviceID against the customer with subscription token tok and
// reports whether the device is allowed under the cap. Same rules as TouchDeviceByLogin.
func (s *Store) TouchDeviceByToken(tok, deviceID string, limit int) (allowed bool, count int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.touchDeviceLocked(s.byTok[tok], deviceID, limit)
}

// TouchDeviceByLogin records deviceID against the customer with the given login and reports
// whether it is allowed under the cap. Rules: a device already seen is always allowed (an
// idempotent re-poll); a NEW device is allowed and recorded only while the distinct-device
// count is below limit; limit <= 0 means unlimited (always allowed + recorded). The boolean
// is the gate; count is the resulting distinct-device count. A device beyond the cap is NOT
// recorded, so the same blocked install keeps being refused without inflating the set.
func (s *Store) TouchDeviceByLogin(login, deviceID string, limit int) (allowed bool, count int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.touchDeviceLocked(s.byLog[login], deviceID, limit)
}

// deviceTTL: a device not seen for this long drops off the cap. Before this, device ids were
// per-INSTALL (regenerated on reinstall) and NEVER expired, so ghosts from past reinstalls piled up
// forever and tripped the 5-device cap. An actively-used device re-touches every /sub poll, so this
// only ever drops truly-gone devices.
const deviceTTL = 60 * 24 * time.Hour

func (s *Store) touchDeviceLocked(c *Customer, deviceID string, limit int) (bool, int, error) {
	if c == nil {
		return false, 0, ErrNotFound
	}
	if c.Devices == nil {
		c.Devices = map[string]time.Time{}
	}
	now := time.Now()
	// Prune stale devices (ghost ids from old reinstalls) so they don't occupy cap slots forever.
	structural := false
	for id, seen := range c.Devices {
		if now.Sub(seen) > deviceTTL {
			delete(c.Devices, id)
			structural = true
		}
	}
	if _, seen := c.Devices[deviceID]; seen {
		c.Devices[deviceID] = now // refresh last-seen so an active device never ages out
		// Persist only on a structural change (a prune) — NOT on every poll's timestamp refresh,
		// which would hammer the JSON store; the in-memory refresh persists on the next structural flush.
		if structural {
			return true, len(c.Devices), s.flush()
		}
		return true, len(c.Devices), nil
	}
	if limit > 0 && len(c.Devices) >= limit {
		if structural {
			_ = s.flush()
		}
		return false, len(c.Devices), nil
	}
	c.Devices[deviceID] = now
	return true, len(c.Devices), s.flush()
}

// ResetDevices clears a customer's recorded device set — a support action for a customer who
// legitimately replaced hardware and hit the cap. The next polls re-populate it from scratch.
func (s *Store) ResetDevices(login string) (*Customer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byLog[login]
	if !ok {
		return nil, ErrNotFound
	}
	c.Devices = nil
	return c.clone(), s.flush()
}

// Delete removes a customer from the store entirely (both indexes) and persists. Idempotent:
// deleting an absent login is a no-op success. NB: this only removes the panel record — node
// creds (VLESS/hy2/…) are deprovisioned separately by the provisioner.
func (s *Store) Delete(login string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byLog[login]
	if !ok {
		return nil
	}
	delete(s.byTok, c.SubToken)
	delete(s.byLog, login)
	return s.flush()
}

// SetDisabled toggles a customer's disabled flag (a soft on/off: a disabled account fails
// Active() so /sub stops serving it, without touching the node creds). Atomic + persisted.
func (s *Store) SetDisabled(login string, disabled bool) (*Customer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byLog[login]
	if !ok {
		return nil, ErrNotFound
	}
	c.Disabled = disabled
	return c.clone(), s.flush()
}

// SetExpiry sets the customer's expiry to an ABSOLUTE time (no stacking) and clears the
// disabled flag. Used to MIRROR an authoritative date set elsewhere (e.g. by the s2 naive
// bot, which owns the naive lifecycle) into the store so the app + the customer's other
// protocols match — without double-counting days the way Extend (which stacks) would.
func (s *Store) SetExpiry(login string, t time.Time) (*Customer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byLog[login]
	if !ok {
		return nil, ErrNotFound
	}
	c.Expires = t
	c.Disabled = false
	return c.clone(), s.flush()
}
