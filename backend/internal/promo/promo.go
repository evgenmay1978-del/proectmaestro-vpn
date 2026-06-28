// Package promo backs the in-app free TRIAL (owner: "install → enter a nickname → get 2 days").
// The hard part is anti-abuse on an ANONYMOUS trial: without Google Play / a verified identity it
// cannot be fully PREVENTED, only made not-worth-it. The primary defense is a persistent ledger
// keyed on the device's ANTI-ABUSE ANCHOR (Android's SSAID + Widevine id + model, sent by the app),
// which — unlike the app's resettable SharedPreferences UUID — survives an app reinstall/clear-data,
// so the 95% case ("uninstalled, reinstalled, tried again") is stopped. A determined abuser
// (factory reset / a second physical box) still gets through — that is the physical ceiling of an
// anonymous trial on a sideloaded, no-GMS fleet; the short 2-day value is the real deterrent.
//
// The ledger stores ONLY a server-salted HMAC of the anchor (never the raw device id), so the file
// can't be used to re-identify devices. JSON-backed, atomic write, mirroring internal/order|store.
package promo

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"
)

// Sentinel errors the API maps to HTTP codes.
var (
	ErrAnchorUsed = errors.New("promo: trial already used on this device") // → 403 (the main gate)
	ErrNickUsed   = errors.New("promo: nickname already used a trial")     // → 409
)

// Redemption is one audit record.
type Redemption struct {
	At         time.Time `json:"at"`
	Login      string    `json:"login"`
	Nick       string    `json:"nick"`
	AnchorHash string    `json:"anchor_hash"`
	IPNet      string    `json:"ip_net"` // the /24 the trial came from (abuse forensics)
}

// Store is the persistent trial ledger: anchorHash → login (one trial per device ever) and
// nick → login (a nickname can seed only one trial). Safe for concurrent use.
type Store struct {
	mu              sync.Mutex
	salt            []byte
	path            string
	RedeemedAnchors map[string]string
	RedeemedNicks   map[string]string
	Audit           []Redemption
}

type fileData struct {
	RedeemedAnchors map[string]string `json:"redeemed_anchors"`
	RedeemedNicks   map[string]string `json:"redeemed_nicks"`
	Audit           []Redemption      `json:"audit"`
}

// Open loads (or creates) the ledger. salt seeds the HMAC over device anchors.
func Open(path, salt string) (*Store, error) {
	s := &Store{
		path: path, salt: []byte(salt),
		RedeemedAnchors: map[string]string{}, RedeemedNicks: map[string]string{},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("promo: read %s: %w", path, err)
	}
	if len(data) > 0 {
		var fd fileData
		if err := json.Unmarshal(data, &fd); err != nil {
			return nil, fmt.Errorf("promo: parse %s: %w", path, err)
		}
		if fd.RedeemedAnchors != nil {
			s.RedeemedAnchors = fd.RedeemedAnchors
		}
		if fd.RedeemedNicks != nil {
			s.RedeemedNicks = fd.RedeemedNicks
		}
		s.Audit = fd.Audit
	}
	return s, nil
}

func (s *Store) persist() error {
	fd := fileData{RedeemedAnchors: s.RedeemedAnchors, RedeemedNicks: s.RedeemedNicks, Audit: s.Audit}
	data, err := json.MarshalIndent(fd, "", "  ")
	if err != nil {
		return fmt.Errorf("promo: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("promo: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("promo: rename: %w", err)
	}
	return nil
}

// Hash is the server-salted HMAC of a raw device anchor (so the ledger never stores raw ids).
func (s *Store) Hash(anchor string) string {
	m := hmac.New(sha256.New, s.salt)
	m.Write([]byte(anchor))
	return hex.EncodeToString(m.Sum(nil))
}

// NormNick canonicalises a nickname for the one-trial-per-nick reservation.
func NormNick(nick string) string { return strings.ToLower(strings.TrimSpace(nick)) }

// Reserve atomically applies the anti-abuse gate and PRE-marks the redemption (race-safe against a
// double tap). Returns ErrAnchorUsed if this device already trialed, ErrNickUsed if the nick was
// used. The caller MUST then Confirm on a successful provision, or Release if provisioning fails
// (so a failed grant doesn't burn the device's one trial).
func (s *Store) Reserve(anchorHash, nick string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.RedeemedAnchors[anchorHash]; ok {
		return ErrAnchorUsed
	}
	n := NormNick(nick)
	if _, ok := s.RedeemedNicks[n]; ok {
		return ErrNickUsed
	}
	s.RedeemedAnchors[anchorHash] = "" // "" = reserved/pending until Confirm
	s.RedeemedNicks[n] = ""
	if err := s.persist(); err != nil {
		delete(s.RedeemedAnchors, anchorHash)
		delete(s.RedeemedNicks, n)
		return err
	}
	return nil
}

// Confirm finalises a reserved redemption with the granted login + an audit record.
func (s *Store) Confirm(anchorHash, nick, login, ipNet string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := NormNick(nick)
	s.RedeemedAnchors[anchorHash] = login
	s.RedeemedNicks[n] = login
	s.Audit = append(s.Audit, Redemption{At: time.Now(), Login: login, Nick: nick, AnchorHash: anchorHash, IPNet: ipNet})
	return s.persist()
}

// Release rolls back a reservation whose provisioning failed, so the device keeps its one trial.
// Only clears entries still in the "" (pending) state — never a confirmed login.
func (s *Store) Release(anchorHash, nick string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.RedeemedAnchors[anchorHash]; ok && v == "" {
		delete(s.RedeemedAnchors, anchorHash)
	}
	n := NormNick(nick)
	if v, ok := s.RedeemedNicks[n]; ok && v == "" {
		delete(s.RedeemedNicks, n)
	}
	return s.persist()
}

// Token returns an n-char uppercase-alphanumeric id (no ambiguous 0/O/1/I) for trial logins.
func Token(n int) string {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[idx.Int64()]
	}
	return string(b)
}
