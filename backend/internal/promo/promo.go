// Package promo backs the in-app free TRIAL (owner: "install → enter a nickname → get 2 days").
// The trial account is keyed on the nickname (login = "trial-"+nick) so it is PERSISTENT and
// REUSABLE — the same person coming back (re-entering their nick, or paying) keeps the SAME
// account instead of spawning duplicates ("не нужно плодить"). Anti-abuse on the anonymous trial
// is a per-DEVICE ledger keyed on the device's reinstall-surviving ANCHOR (Android SSAID +
// Widevine id + model, sent by the app): one trial account per device, ever. Unlike the app's
// resettable UUID, the anchor survives an app reinstall/clear-data, so the 95% "reinstall and
// trial again" case is stopped. A factory reset / a second physical device still gets through —
// the physical ceiling of an anonymous trial on a sideloaded, no-GMS fleet; the short 2-day value
// is the real deterrent. The ledger stores only a server-salted HMAC of the anchor (never the raw
// device id). JSON-backed, atomic write, mirroring internal/order|store.
package promo

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Redemption is one audit record.
type Redemption struct {
	At         time.Time `json:"at"`
	Login      string    `json:"login"`
	Nick       string    `json:"nick"`
	AnchorHash string    `json:"anchor_hash"`
	IPNet      string    `json:"ip_net"` // the /24 the trial came from (abuse forensics)
}

// Store is the persistent trial ledger: anchorHash → the trial login that device got (one ever).
type Store struct {
	mu              sync.Mutex
	salt            []byte
	path            string
	RedeemedAnchors map[string]string
	Audit           []Redemption
}

type fileData struct {
	RedeemedAnchors map[string]string `json:"redeemed_anchors"`
	Audit           []Redemption      `json:"audit"`
}

// Open loads (or creates) the ledger. salt seeds the HMAC over device anchors.
func Open(path, salt string) (*Store, error) {
	s := &Store{path: path, salt: []byte(salt), RedeemedAnchors: map[string]string{}}
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
		s.Audit = fd.Audit
	}
	return s, nil
}

func (s *Store) persist() error {
	fd := fileData{RedeemedAnchors: s.RedeemedAnchors, Audit: s.Audit}
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

// NormNick canonicalises a nickname into the trial login SUFFIX. It lowercases, trims, and strips
// every character outside [a-z0-9_-] — the login becomes a Hysteria (viper) userpass key, where a
// "." nests the map and crashes Hy2 for the WHOLE fleet (see server2.hy2SafeUser; a "trial-roman.pfa"
// login did exactly that on 2026-07-02). Returns "" if nothing survives (the caller then rejects).
func NormNick(nick string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(nick)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// AnchorLogin returns the trial login this device already received, or "" if it never trialed.
func (s *Store) AnchorLogin(anchorHash string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.RedeemedAnchors[anchorHash]
}

// SetAnchor records that a device (anchor) received a trial account, with an audit entry.
func (s *Store) SetAnchor(anchorHash, login, nick, ipNet string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RedeemedAnchors[anchorHash] = login
	s.Audit = append(s.Audit, Redemption{At: time.Now(), Login: login, Nick: nick, AnchorHash: anchorHash, IPNet: ipNet})
	return s.persist()
}
