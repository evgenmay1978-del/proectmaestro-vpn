// Package runtimefence contains the default-deny commercial Xray dispatcher.
package runtimefence

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/features/stats"
)

const (
	ManagedInbound  = "maestro-cdn-in"
	maxUsers        = 4096
	maxSessions     = 4096
	maxUserSessions = 128
	maxDrain        = 2 * time.Second
)

var errDenied = errors.New("managed session denied")

// Control is a compare-and-advance operation bound to this physical process and
// its exact input configuration. Retries must retain the same generation/op.
type Control struct {
	Schema       int    `json:"schema"`
	Operation    string `json:"operation"`
	Email        string `json:"email"`
	BootID       string `json:"boot_id"`
	ConfigDigest string `json:"config_digest"`
	Generation   uint64 `json:"generation"`
}

type Receipt struct {
	Schema        int    `json:"schema"`
	State         string `json:"state"`
	Email         string `json:"email"`
	BootID        string `json:"boot_id"`
	ConfigDigest  string `json:"config_digest"`
	Generation    uint64 `json:"generation"`
	ResetSequence uint64 `json:"reset_sequence"`
	ObservedAt    string `json:"observed_at"`
	Uplink        *int64 `json:"uplink,omitempty"`
	Downlink      *int64 `json:"downlink,omitempty"`
}

type userState struct {
	generation uint64
	operation  string
	allowed    bool
	user       *protocol.MemoryUser
	sessions   map[*stream]struct{}
}

type gate struct {
	mu           sync.Mutex
	boot, digest string
	users        map[string]*userState
	count        int
	closed       bool
	changed      chan struct{}
}

func newGate(boot, digest string) *gate {
	return &gate{boot: boot, digest: digest, users: make(map[string]*userState), changed: make(chan struct{})}
}

func validEmail(email string) bool {
	parts := strings.Split(email, ":")
	if len(email) > 200 || len(parts) != 3 || parts[0] != "wl" || parts[1] == "" || len(parts[2]) != 7 || !strings.HasPrefix(parts[2], "exit-s") || parts[2][6] < '1' || parts[2][6] > '4' {
		return false
	}
	for _, r := range email {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '>' {
			return false
		}
	}
	return true
}

func (g *gate) notifyLocked() { close(g.changed); g.changed = make(chan struct{}) }

func (g *gate) apply(ctx context.Context, c Control, user *protocol.MemoryUser, sm stats.Manager) (*Receipt, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if c.Schema != 1 || !validEmail(c.Email) || c.BootID != g.boot || c.ConfigDigest != g.digest || c.Generation == 0 || (c.Operation != "grant" && c.Operation != "fence") {
		return nil, errDenied
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, errDenied
	}
	u := g.users[c.Email]
	if u == nil {
		if len(g.users) >= maxUsers {
			g.mu.Unlock()
			return nil, errDenied
		}
		u = &userState{sessions: make(map[*stream]struct{})}
		g.users[c.Email] = u
	}
	if c.Generation < u.generation || (c.Generation == u.generation && c.Operation != u.operation) {
		g.mu.Unlock()
		return nil, errDenied
	}
	if c.Operation == "grant" {
		if user == nil || user.Email != c.Email {
			g.mu.Unlock()
			return nil, errDenied
		}
		if c.Generation == u.generation {
			if !u.allowed || u.user != user {
				g.mu.Unlock()
				return nil, errDenied
			}
		} else {
			// A removed/re-added user has a different MemoryUser. Old idle mux
			// contexts retain the old pointer and must never inherit a new grant.
			if u.allowed || len(u.sessions) != 0 || (u.user != nil && u.user == user) {
				g.mu.Unlock()
				return nil, errDenied
			}
			u.generation, u.operation, u.allowed, u.user = c.Generation, c.Operation, true, user
		}
		g.mu.Unlock()
		return g.receipt(c, "granted"), nil
	}
	u.generation, u.operation, u.allowed = c.Generation, c.Operation, false
	for s := range u.sessions {
		s.stopLocked()
	}
	g.notifyLocked()
	deadline, cancel := context.WithTimeout(ctx, maxDrain)
	defer cancel()
	for len(u.sessions) != 0 {
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-deadline.Done():
			return nil, deadline.Err()
		case <-changed:
		}
		g.mu.Lock()
		if u.generation != c.Generation || u.allowed || g.closed {
			g.mu.Unlock()
			return nil, errDenied
		}
	}
	// The gate lock linearizes the final read with grants and all I/O starts.
	// Every old stream is closed and removed only after worker/I/O/stop drain.
	if deadline.Err() != nil {
		g.mu.Unlock()
		return nil, deadline.Err()
	}
	up, down := sm.GetCounter(counterName(c.Email, "uplink")), sm.GetCounter(counterName(c.Email, "downlink"))
	if up == nil || down == nil {
		g.mu.Unlock()
		return nil, errors.New("final counters unavailable")
	}
	uv, dv := up.Value(), down.Value()
	if uv < 0 || dv < 0 {
		g.mu.Unlock()
		return nil, errors.New("invalid final counters")
	}
	r := g.receipt(c, "fenced")
	r.Uplink, r.Downlink = &uv, &dv
	g.mu.Unlock()
	return r, nil
}

func (g *gate) receipt(c Control, state string) *Receipt {
	return &Receipt{Schema: 1, State: state, Email: c.Email, BootID: g.boot, ConfigDigest: g.digest, Generation: c.Generation, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}

func counterName(email, direction string) string {
	return "user>>>" + email + ">>>traffic>>>" + direction
}

type stream struct {
	gate                                    *gate
	owner                                   *userState
	cancel                                  context.CancelFunc
	interrupt                               func()
	closing, stopping, workerDone, stopDone bool
	active                                  int
}

func (g *gate) start(ctx context.Context, user *protocol.MemoryUser, interrupt func()) (context.Context, *stream, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	u := g.users[user.Email]
	if g.closed || u == nil || !u.allowed || u.user != user || g.count >= maxSessions || len(u.sessions) >= maxUserSessions {
		return nil, nil, errDenied
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &stream{gate: g, owner: u, cancel: cancel, interrupt: interrupt}
	u.sessions[s] = struct{}{}
	g.count++
	return ctx, s, nil
}

func (s *stream) begin() bool {
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	if s.closing || !s.owner.allowed || s.gate.closed {
		return false
	}
	s.active++
	return true
}

func (s *stream) end() { s.gate.mu.Lock(); defer s.gate.mu.Unlock(); s.active--; s.collectLocked() }

func (s *stream) finish() {
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	// A normally completed mux child must neither close its shared parent
	// connection nor discard already-counted queued DOWN bytes. Closing this
	// entry prevents every subsequent counted operation; an outstanding read
	// retains it so an explicit fence can still escalate to interruption.
	s.workerDone, s.closing = true, true
	if !s.stopping {
		s.stopDone = true
	}
	s.cancel()
	s.collectLocked()
}

func (s *stream) stopLocked() {
	if s.stopping {
		return
	}
	s.closing, s.stopping, s.stopDone = true, true, false
	// Close/Interrupt implementations may block. Their bounded registry slot is
	// retained and a fence times out rather than returning an invented receipt.
	go func() {
		s.cancel()
		s.interrupt()
		s.gate.mu.Lock()
		defer s.gate.mu.Unlock()
		s.stopDone = true
		s.collectLocked()
	}()
}

func (s *stream) collectLocked() {
	if !s.workerDone || !s.stopDone || s.active != 0 {
		return
	}
	if _, exists := s.owner.sessions[s]; exists {
		delete(s.owner.sessions, s)
		s.gate.count--
		s.gate.notifyLocked()
	}
}

func (g *gate) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	for _, u := range g.users {
		u.allowed = false
		for s := range u.sessions {
			s.stopLocked()
		}
	}
	g.notifyLocked()
}
