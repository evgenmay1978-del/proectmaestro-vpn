// Package olcconf is the SINGLE SOURCE OF TRUTH for the olcRTC fallback transport's
// runtime parameters (carrier room URL + shared key + provider/transport).
//
// PER-LOGIN ROOMS (isolation): a Telemost room + srv is a 1-participant channel — if two
// gated logins (the owner's family: wapmix/wapmixx/wapmix2) join the SAME room+srv at once
// they cross-latch (wrong-peer / zombie peers / liveness-death). So each login can be given
// its OWN {room,key} in Rooms[login], served by its OWN srv instance on S3. A login WITHOUT
// a dedicated entry falls back to the GLOBAL Room/Key (the shared default). Yandex Telemost
// has no room-creation API (rooms are born in the Yandex UI), so the owner supplies each
// room URL; this package just routes the right room+key to the right login.
//
// Why a hot-reloadable FILE (not env): Yandex Telemost rooms can expire, and a new room
// must propagate WITHOUT a panel redeploy. Rooms are updated at runtime via the admin
// endpoint (POST /admin/olcrtc/room, optional "login") → persisted here → the next /sub and
// /info polls serve the new room automatically. A one-command swap (ops/olcrtc-room.sh
// [login] <url>) also pushes the room to that login's S3 srv, so both consumers update from
// a single action.
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

// RoomKey is one carrier room + its shared key (a per-login isolation channel).
type RoomKey struct {
	Room string `json:"room"` // carrier room id/URL (Telemost: the https://…/j/<id> URL)
	Key  string `json:"key"`  // 64-hex shared secret (must byte-match this login's S3 srv)
}

// Config is the olcRTC transport configuration. Provider/Transport are shared; Room/Key are
// the GLOBAL default; Rooms holds per-login {room,key} overrides for isolation.
type Config struct {
	Enabled   bool               `json:"enabled"`          // master switch; false → olcRTC never emitted
	Provider  string             `json:"provider"`         // "telemost" (RU production) | "jitsi"
	Room      string             `json:"room"`             // GLOBAL default room (fallback for logins w/o a dedicated one)
	Key       string             `json:"key"`              // GLOBAL default key
	Transport string             `json:"transport"`        // "vp8channel" (RU/mobile) | "datachannel"
	Rooms     map[string]RoomKey `json:"rooms,omitempty"`  // login → its OWN {room,key} (isolation); empty → use global
	Logins    []string           `json:"logins,omitempty"` // the olcRTC ALLOWLIST — WHO gets olcRTC creds in /sub+/info
}

// Allowed reports whether login is on the olcRTC allowlist.
func (c Config) Allowed(login string) bool {
	for _, l := range c.Logins {
		if l == login {
			return true
		}
	}
	return false
}

// Ready reports whether olcRTC is enabled AND has at least the GLOBAL room/key configured.
// (Kept for the global-config invariant; per-login emission uses RoomFor, which also accepts
// a login that only has a dedicated room even when the global default is unset.)
func (c Config) Ready() bool { return c.Enabled && c.Room != "" && c.Key != "" }

// RoomFor returns the room+key a given login should use: its dedicated per-login room when
// configured (both room and key non-empty), else the GLOBAL default. ok is false only when
// NEITHER a per-login nor a global room+key is set (so the caller emits nothing). Callers
// must still check Enabled and the MAESTRO_OLC_LOGINS gate.
func (c Config) RoomFor(login string) (room, key string, ok bool) {
	if rk, has := c.Rooms[login]; has && rk.Room != "" && rk.Key != "" {
		return rk.Room, rk.Key, true
	}
	if c.Room != "" && c.Key != "" {
		return c.Room, c.Key, true
	}
	return "", "", false
}

// Dedicated reports whether login has its OWN room (not the shared global fallback) — used
// by ops/inspection to know which logins are isolated.
func (c Config) Dedicated(login string) bool {
	rk, has := c.Rooms[login]
	return has && rk.Room != "" && rk.Key != ""
}

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
	b, err := os.ReadFile(path) //#nosec G304 -- path is the server-configured olcconf file (cfg/env), never request-derived
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

// SetRoom swaps ONLY the GLOBAL room URL (the common case when the shared Telemost room
// expires), keeping the key/provider/transport. Auto-enables once room+key are both set.
// Thin wrapper over SetRoomFor("", …) so the auto-enable logic lives in one place.
func (s *Store) SetRoom(room string) error { return s.SetRoomFor("", room, "") }

// SetRoomFor assigns login its OWN room+key (isolation channel), overriding the global
// default for that login only. An empty room REMOVES the override (login reverts to the
// global room). An empty key keeps the login's existing key (so a room-only swap doesn't
// wipe the key + desync the S3 srv). Auto-enables once any usable room+key exists. Persists.
func (s *Store) SetRoomFor(login, room, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if login == "" { // no login → this is a global-room swap
		s.cfg.Room = room
		if key != "" {
			s.cfg.Key = key
		}
	} else {
		if s.cfg.Rooms == nil {
			s.cfg.Rooms = map[string]RoomKey{}
		}
		if room == "" {
			delete(s.cfg.Rooms, login)
		} else {
			rk := s.cfg.Rooms[login]
			rk.Room = room
			if key != "" {
				rk.Key = key
			}
			s.cfg.Rooms[login] = rk
		}
	}
	// Auto-enable once at least one login can be served (global default OR any per-login room).
	if !s.cfg.Enabled && s.hasAnyReadyLocked() {
		s.cfg.Enabled = true
	}
	return s.persistLocked()
}

// hasAnyReadyLocked reports whether ANY usable room+key exists (global or per-login).
func (s *Store) hasAnyReadyLocked() bool {
	if s.cfg.Room != "" && s.cfg.Key != "" {
		return true
	}
	for _, rk := range s.cfg.Rooms {
		if rk.Room != "" && rk.Key != "" {
			return true
		}
	}
	return false
}

// SetLogins replaces the whole olcRTC allowlist (deduped, order preserved) and persists.
func (s *Store) SetLogins(logins []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	out := make([]string, 0, len(logins))
	for _, l := range logins {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	s.cfg.Logins = out
	return s.persistLocked()
}

// AddLogin grants login olcRTC access (no-op if already present). Persists.
func (s *Store) AddLogin(login string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if login == "" || s.cfg.Allowed(login) {
		return nil
	}
	s.cfg.Logins = append(s.cfg.Logins, login)
	return s.persistLocked()
}

// RemoveLogin revokes login's olcRTC access AND drops its dedicated room (so it stops getting
// creds and its per-login entry doesn't linger). Persists.
func (s *Store) RemoveLogin(login string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.cfg.Logins[:0:0]
	for _, l := range s.cfg.Logins {
		if l != login {
			out = append(out, l)
		}
	}
	s.cfg.Logins = out
	delete(s.cfg.Rooms, login)
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
