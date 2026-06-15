// Package order is the in-app purchase flow for the TV app: the customer picks a
// tariff, the backend creates a pending order with СБП payment details, the owner
// confirms payment (which provisions the customer), and the app polls the order
// until it flips to paid and receives its subscription URL.
package order

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sync"
	"time"
)

// ErrNotFound is returned when no order matches.
var ErrNotFound = errors.New("order: not found")

// Tariff is one purchasable plan.
type Tariff struct {
	Key  string `json:"key"`  // "1m","3m","6m","12m"
	Name string `json:"name"` // human label
	Days int    `json:"days"`
	Rub  int    `json:"rub"`
}

// DefaultTariffs — prices per the owner.
var DefaultTariffs = []Tariff{
	{Key: "1m", Name: "1 месяц", Days: 30, Rub: 300},
	{Key: "2m", Name: "2 месяца", Days: 60, Rub: 600},
}

// TariffByKey returns the tariff for a key, or false.
func TariffByKey(key string) (Tariff, bool) {
	for _, t := range DefaultTariffs {
		if t.Key == key {
			return t, true
		}
	}
	return Tariff{}, false
}

// Order is one purchase. Login is the customer id provisioned on payment; Code is
// the СБП payment comment the customer includes so the owner can match the payment.
type Order struct {
	ID        string    `json:"id"`
	Tariff    string    `json:"tariff"`
	Days      int       `json:"days"`
	Rub       int       `json:"rub"`
	Code      string    `json:"code"`   // СБП comment, short + human-typeable
	Login     string    `json:"login"`  // customer login provisioned on confirm
	Status    string    `json:"status"` // "pending" | "paid"
	SubToken  string    `json:"sub_token"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is a JSON-file-backed order registry, safe for concurrent use.
type Store struct {
	path string
	mu   sync.RWMutex
	byID map[string]*Order
}

// Open loads (or creates) the order store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, byID: map[string]*Order{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("order: read %s: %w", path, err)
	}
	var list []*Order
	if len(data) > 0 {
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("order: parse %s: %w", path, err)
		}
	}
	for _, o := range list {
		s.byID[o.ID] = o
	}
	return s, nil
}

func (s *Store) flush() error {
	list := make([]*Order, 0, len(s.byID))
	for _, o := range s.byID {
		list = append(list, o)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("order: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("order: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("order: rename: %w", err)
	}
	return nil
}

// Create makes a new pending order for the tariff.
func (s *Store) Create(t Tariff) (*Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Lazy GC: every new order drops abandoned pending orders (a "Купить" tap that
	// was never paid) older than 24h, so orders.json can't grow without bound.
	s.purgeStaleLocked(24 * time.Hour)
	o := &Order{
		ID:        "ord_" + token(16),
		Tariff:    t.Key,
		Days:      t.Days,
		Rub:       t.Rub,
		Code:      "M" + token(5),
		Login:     "tv-" + token(8),
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	s.byID[o.ID] = o
	return o, s.flush()
}

// ByID returns the order.
func (s *Store) ByID(id string) (*Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if o, ok := s.byID[id]; ok {
		return o, nil
	}
	return nil, ErrNotFound
}

// MarkPaid records the provisioned customer's sub token and flips status.
func (s *Store) MarkPaid(id, subToken string) (*Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	o.Status = "paid"
	o.SubToken = subToken
	return o, s.flush()
}

// Cancel removes a still-pending order (the owner saw no payment). A paid order
// is left intact. Idempotent: a missing order is a no-op (already cleaned up).
func (s *Store) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.byID[id]
	if !ok {
		return nil
	}
	if o.Status == "paid" {
		return errors.New("order: cannot cancel a paid order")
	}
	delete(s.byID, id)
	return s.flush()
}

// purgeStaleLocked drops pending orders older than maxAge. Caller holds the lock.
func (s *Store) purgeStaleLocked(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	for id, o := range s.byID {
		if o.Status == "pending" && o.CreatedAt.Before(cutoff) {
			delete(s.byID, id)
		}
	}
}

// List returns a snapshot of all orders.
func (s *Store) List() []*Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Order, 0, len(s.byID))
	for _, o := range s.byID {
		out = append(out, o)
	}
	return out
}

// token returns an n-char uppercase-alphanumeric id (no ambiguous 0/O/1/I).
func token(n int) string {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[idx.Int64()]
	}
	return string(b)
}
