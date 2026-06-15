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
	Login    string             `json:"login"`
	SubToken string             `json:"sub_token"`
	Expires  time.Time          `json:"expires"`
	Disabled bool               `json:"disabled"`
	VLESS    *subgen.VLESSCreds `json:"vless,omitempty"`
	Hy2      *subgen.Hy2Creds   `json:"hy2,omitempty"`
	Naive    *subgen.NaiveCreds `json:"naive,omitempty"`
	Mieru    *subgen.MieruCreds `json:"mieru,omitempty"`
}

// Active reports whether the subscription is usable right now.
func (c *Customer) Active() bool {
	return !c.Disabled && time.Now().Before(c.Expires)
}

// ToSubgen maps a customer to the subscription generator input.
func (c *Customer) ToSubgen() subgen.Customer {
	return subgen.Customer{Name: c.Login, VLESS: c.VLESS, Hy2: c.Hy2, Naive: c.Naive, Mieru: c.Mieru}
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
	if c.Mieru != nil {
		m := *c.Mieru
		cp.Mieru = &m
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
	data, err := os.ReadFile(path)
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
