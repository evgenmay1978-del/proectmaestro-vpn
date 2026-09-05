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
	maxLease        = 5 * time.Second
)

var errDenied = errors.New("managed session denied")

// Control is a compare-and-advance operation bound to this physical process and
// its exact input configuration. Generation versions control operations, not
// durable desired state. Retries retain generation, operation and lease length.
type Control struct {
	Schema       int    `json:"schema"`
	Operation    string `json:"operation"`
	Email        string `json:"email"`
	BootID       string `json:"boot_id"`
	ConfigDigest string `json:"config_digest"`
	Generation   uint64 `json:"generation"`
	LeaseMS      uint32 `json:"lease_ms,omitempty"`
}

type Receipt struct {
	Schema           int     `json:"schema"`
	State            string  `json:"state"`
	Email            string  `json:"email"`
	BootID           string  `json:"boot_id"`
	ConfigDigest     string  `json:"config_digest"`
	Generation       uint64  `json:"generation"`
	ResetSequence    uint64  `json:"reset_sequence"`
	ObservedAt       string  `json:"observed_at"`
	Uplink           *int64  `json:"uplink,omitempty"`
	Downlink         *int64  `json:"downlink,omitempty"`
	LeaseExpiresAt   string  `json:"lease_expires_at,omitempty"`
	LeaseRemainingMS *uint32 `json:"lease_remaining_ms,omitempty"`
}

type userState struct {
	generation uint64
	operation  string
	allowed    bool
	// Retained for this email's entire physical boot, including regrants.
	everStarted   bool
	user          *protocol.MemoryUser
	sessions      map[*stream]struct{}
	leaseMS       uint32
	leaseDeadline time.Time
	leaseTimer    *time.Timer
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

func (g *gate) fenceLocked(u *userState) {
	u.allowed = false
	if u.leaseTimer != nil {
		u.leaseTimer.Stop()
		u.leaseTimer = nil
	}
	for s := range u.sessions {
		s.stopLocked()
	}
	g.notifyLocked()
}

func (g *gate) expireLocked(u *userState, now time.Time) {
	if u != nil && u.allowed && !now.Before(u.leaseDeadline) {
		g.fenceLocked(u)
	}
}

func (g *gate) expireLease(u *userState, generation uint64, deadline time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Stop can race an already scheduled callback. A previous lease cannot
	// fence a newer renewal or a replacement user's grant.
	if g.closed || u.generation != generation || !u.leaseDeadline.Equal(deadline) {
		return
	}
	g.expireLocked(u, time.Now())
}

func (g *gate) armLeaseLocked(u *userState, c Control, now time.Time) {
	if u.leaseTimer != nil {
		u.leaseTimer.Stop()
	}
	u.leaseMS = c.LeaseMS
	u.leaseDeadline = now.Add(time.Duration(c.LeaseMS) * time.Millisecond)
	deadline, generation := u.leaseDeadline, u.generation
	u.leaseTimer = time.AfterFunc(time.Until(deadline), func() { g.expireLease(u, generation, deadline) })
}

func (g *gate) leaseReceiptLocked(u *userState, c Control) (*Receipt, error) {
	now := time.Now()
	g.expireLocked(u, now)
	if !u.allowed {
		return nil, errDenied
	}
	// Floor, never ceil: callers use request-start monotonic time plus this
	// remaining duration. The wall-clock expiry is only a diagnostic hint.
	remaining := uint32(u.leaseDeadline.Sub(now) / time.Millisecond)
	r := g.receipt(c, "granted")
	r.LeaseExpiresAt = u.leaseDeadline.UTC().Format(time.RFC3339Nano)
	r.LeaseRemainingMS = &remaining
	return r, nil
}

func (g *gate) apply(ctx context.Context, c Control, user *protocol.MemoryUser, sm stats.Manager) (*Receipt, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	leasedOperation := c.Operation == "grant" || c.Operation == "renew"
	if c.Schema != 1 || !validEmail(c.Email) || c.BootID != g.boot || c.ConfigDigest != g.digest || c.Generation == 0 || (!leasedOperation && c.Operation != "fence") || (leasedOperation && (c.LeaseMS == 0 || c.LeaseMS > uint32(maxLease/time.Millisecond))) || (c.Operation == "fence" && c.LeaseMS != 0) {
		return nil, errDenied
	}
	g.mu.Lock()
	if g.closed || ctx.Err() != nil {
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
	// A late renewal cannot win by getting the mutex before the timer callback.
	g.expireLocked(u, time.Now())
	if c.Generation < u.generation || (c.Generation == u.generation && c.Operation != u.operation) {
		g.mu.Unlock()
		return nil, errDenied
	}
	if leasedOperation {
		if user == nil || user.Email != c.Email {
			g.mu.Unlock()
			return nil, errDenied
		}
		if c.Generation == u.generation {
			if !u.allowed || u.user != user || u.leaseMS != c.LeaseMS {
				g.mu.Unlock()
				return nil, errDenied
			}
		} else if c.Operation == "renew" {
			now := time.Now()
			g.expireLocked(u, now)
			if !u.allowed || u.user != user {
				g.mu.Unlock()
				return nil, errDenied
			}
			u.generation, u.operation = c.Generation, c.Operation
			g.armLeaseLocked(u, c, now)
		} else {
			// A removed/re-added user has a different MemoryUser. Old idle mux
			// contexts retain the old pointer and must never inherit a new grant.
			if u.allowed || len(u.sessions) != 0 || (u.user != nil && u.user == user) {
				g.mu.Unlock()
				return nil, errDenied
			}
			u.generation, u.operation, u.allowed, u.user = c.Generation, c.Operation, true, user
			g.armLeaseLocked(u, c, time.Now())
		}
		r, err := g.leaseReceiptLocked(u, c)
		g.mu.Unlock()
		return r, err
	}
	u.generation, u.operation, u.allowed = c.Generation, c.Operation, false
	g.fenceLocked(u)
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
	if up == nil && down == nil && !u.everStarted {
		// Admission is closed and the registry has fully drained above. With
		// no successful start in this physical boot, absence is proven unused,
		// not a fabricated zero sample or an accounting boundary timestamp.
		r := g.receipt(c, "fenced_unused")
		g.mu.Unlock()
		return r, nil
	}
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
	g.expireLocked(u, time.Now())
	if g.closed || u == nil || !u.allowed || u.user != user || g.count >= maxSessions || len(u.sessions) >= maxUserSessions {
		return nil, nil, errDenied
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &stream{gate: g, owner: u, cancel: cancel, interrupt: interrupt}
	u.everStarted = true
	u.sessions[s] = struct{}{}
	g.count++
	return ctx, s, nil
}

func (s *stream) begin() bool {
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	s.gate.expireLocked(s.owner, time.Now())
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
		g.fenceLocked(u)
	}
	g.notifyLocked()
}
