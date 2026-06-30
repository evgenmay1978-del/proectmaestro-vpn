// Package olcconf is the SINGLE SOURCE OF TRUTH for the olcRTC fallback transport's
// runtime parameters (carrier room URL + shared key + provider/transport). It is GLOBAL,
// not per-customer: there is one olcrtc srv (on S3) joined to one carrier room with one
// shared key, so the room/key are shared by every olcRTC client (currently the owner's
// gated login only).
//
// Why a hot-reloadable FILE (not env): Yandex Telemost rooms can expire, and a new room
// must propagate WITHOUT a panel redeploy. The room is updated at runtime via the admin
// endpoint (POST /admin/olcrtc/room) → persisted here → the next /sub and /info polls
// serve the new room automatically. A one-command swap (ops/olcrtc-room.sh) also pushes
// the new room to the S3 srv, so both consumers update from a single action.
//
// WHO gets olcRTC is a SEPARATE gate (the MAESTRO_OLC_LOGINS allowlist in package api);
// this package only holds WHAT the transport is.
package olcconf

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// Config is the global olcRTC transport configuration.
type Config struct {
	Enabled   bool   `json:"enabled"`   // master switch; false → olcRTC never emitted
	Provider  string `json:"provider"`  // "telemost" (RU production) | "jitsi"
	Room      string `json:"room"`      // carrier room id/URL (Telemost: the j/<id> URL)
	Key       string `json:"key"`       // 64-hex shared secret (must match the S3 srv)
	Transport string `json:"transport"` // "vp8channel" (RU/mobile) | "datachannel"
}

// Ready reports whether olcRTC may be emitted: enabled AND fully configured.
func (c Config) Ready() bool { return c.Enabled && c.Room != "" && c.Key != "" }

// Store is a file-backed, concurrency-safe holder for the global Config.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// Open loads the config from path (JSON). A missing file is fine — it starts empty
// (disabled) with sensible defaults; the first Set persists it. path=="" → in-memory only.
func Open(path string) (*Store, error) {
	s := &Store{path: path, cfg: Config{Provider: "telemost", Transport: "vp8channel"}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	s.cfg = c
	return s, nil
}

// Get returns a snapshot of the current config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Set replaces the whole config (filling provider/transport defaults) and persists it.
func (s *Store) Set(c Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Provider == "" {
		c.Provider = "telemost"
	}
	if c.Transport == "" {
		c.Transport = "vp8channel"
	}
	s.cfg = c
	return s.persistLocked()
}

// SetRoom swaps ONLY the room URL (the common case when a Telemost room expires), keeping
// the key/provider/transport. Auto-enables once room+key are both set. Persists.
func (s *Store) SetRoom(room string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Room = room
	if room != "" && s.cfg.Key != "" {
		s.cfg.Enabled = true
	}
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
