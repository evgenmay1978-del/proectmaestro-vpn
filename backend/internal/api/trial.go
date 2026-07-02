package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/promo"
)

// trialVelocity is a small IN-MEMORY soft rate-limiter: how many NEW trials a /24 subnet started in
// a rolling window. Kept LENIENT on purpose — RU CGNAT/mobile shares IPs, so a hard block would hit
// real users; this only blunts crude single-network farming. Resets on restart (a soft signal).
type trialVelocity struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	hits   map[string][]time.Time
}

func newTrialVelocity(limit int, window time.Duration) *trialVelocity {
	return &trialVelocity{window: window, limit: limit, hits: map[string][]time.Time{}}
}

// allow records a hit for ipNet and reports whether it stays under the limit. limit<=0 disables it.
func (v *trialVelocity) allow(ipNet string) bool {
	if v.limit <= 0 {
		return true
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	cutoff := time.Now().Add(-v.window)
	old := v.hits[ipNet]
	kept := make([]time.Time, 0, len(old)+1)
	for _, t := range old {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= v.limit {
		v.hits[ipNet] = kept
		return false
	}
	v.hits[ipNet] = append(kept, time.Now())
	return true
}

// clientIP is the caller's real IP — the first X-Forwarded-For hop (we sit behind nginx) or RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := strings.TrimSpace(strings.Split(xff, ",")[0]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipNet24 aggregates an IP to its /24 (IPv4) or /48 (IPv6) for velocity bucketing + audit.
func ipNet24(ip string) string {
	p := net.ParseIP(ip)
	if p == nil {
		return ip
	}
	if v4 := p.To4(); v4 != nil {
		return net.IP{v4[0], v4[1], v4[2], 0}.String() + "/24"
	}
	masked := make(net.IP, net.IPv6len)
	copy(masked, p[:6])
	return masked.String() + "/48"
}

// handleTrial is the public POST /trial: an app with NO key enters a nickname and gets a short free
// trial. The trial account is KEYED ON THE NICK (login = "trial-"+nick) so it's persistent and
// reusable — the same person keeps the SAME account (re-entry, renewal) instead of spawning
// duplicates ("не нужно плодить"). Flow:
//  1. validate nick + device anchor;
//  2. if this DEVICE already trialed → return its EXISTING account (re-activate, e.g. after a
//     reinstall) — never a second account;
//  3. else if the nick is already taken by ANOTHER device → 409;
//  4. soft per-/24 velocity → 429;
//  5. provision a short all-protocol account named "trial-<nick>" + record the device in the ledger.
//
// The anchor is the app's ANDROID_ID(+Widevine+model) composite — it survives reinstall (unlike the
// device UUID), which is what stops the 95% "uninstall→reinstall→trial again" case. Renewal is the
// normal pay flow, which extends THIS account (BuyViewModel sends its sub_token) — no new account.
func (s *Server) handleTrial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Nick   string `json:"nick"`
		Anchor string `json:"anchor"`
		Device string `json:"device"` // app UUID (device-cap is enforced later at /sub)
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// 1. validate
	nick := strings.TrimSpace(req.Nick)
	if nick == "" || !claimCodeRe.MatchString(nick) {
		http.Error(w, "invalid nick", http.StatusBadRequest)
		return
	}
	anchor := strings.TrimSpace(req.Anchor)
	androidID := anchor
	if i := strings.IndexByte(anchor, '|'); i >= 0 {
		androidID = anchor[:i]
	}
	if androidID == "" {
		http.Error(w, "device anchor required", http.StatusBadRequest)
		return
	}
	anchorHash := s.promos.Hash(anchor)

	// 2. this DEVICE already trialed → hand back its EXISTING account (don't make a new one).
	if prev := s.promos.AnchorLogin(anchorHash); prev != "" {
		if c, err := s.st.ByLogin(prev); err == nil {
			s.respCustomer(w, c)
			return
		}
		// the recorded account was removed (owner cleanup) → don't issue a fresh trial.
		http.Error(w, "trial already used on this device", http.StatusForbidden)
		return
	}

	// 3. the nick IS the trial account id. If it already exists, it belongs to another device.
	// NormNick strips it to a Hy2/viper-safe charset; reject if nothing usable remains.
	suffix := promo.NormNick(nick)
	if suffix == "" {
		http.Error(w, "invalid nick", http.StatusBadRequest)
		return
	}
	login := "trial-" + suffix
	if _, err := s.st.ByLogin(login); err == nil {
		http.Error(w, "nickname already used", http.StatusConflict)
		return
	}

	// 4. soft velocity (only NEW trials count).
	ipNet := ipNet24(clientIP(r))
	if !s.trialVel.allow(ipNet) {
		http.Error(w, "too many trials from your network, try later", http.StatusTooManyRequests)
		return
	}

	// 5. provision a short all-protocol trial account (same path as a paid order), then record it.
	dur := time.Duration(s.cfg.TrialDays) * 24 * time.Hour
	if dur <= 0 {
		dur = 48 * time.Hour
	}
	c, err := s.prov.Provision(login, dur)
	if err != nil {
		http.Error(w, "could not start trial", http.StatusBadGateway)
		return
	}
	_ = s.promos.SetAnchor(anchorHash, c.Login, nick, ipNet)
	s.respCustomer(w, c)
}
