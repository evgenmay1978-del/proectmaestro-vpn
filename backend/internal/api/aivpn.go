package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

// aivpnOutcome is what aivpnKey found: the key itself, a gate refusal, or a
// device refusal. The two refusals differ only in what /aivpn reports: a capped
// device gets /sub's own "device limit reached" answer, every other refusal
// stays the opaque "aivpn unavailable".
type aivpnOutcome int

const (
	// aivpnOK: every gate passed and the source returned a usable key.
	aivpnOK aivpnOutcome = iota
	// aivpnUnavailable: a gate refused, or the source holds/fetched no key.
	aivpnUnavailable
	// aivpnDeviceLimit: a NEW device would exceed the per-account cap.
	aivpnDeviceLimit
)

// aivpnKey returns the subscriber's AIVPN connection key when EVERY gate passes:
//
//   - the source exists and is switched on (config; nil/off is the default);
//   - the subscription is active — an expired account must not hold a live
//     credential (same rule as olcRTC, whose chip locks on expiry);
//   - the caller is our app and its versionCode is at least the shipped AIVPN
//     build. appVersionCode() returns 0 for curl/Karing/any non-SFA UA, so those
//     can never match even if the minimum were misconfigured to 0;
//   - the login is on the issuance whitelist;
//   - the device, when the app sends one, fits the same per-account cap /sub
//     enforces (a NEW device beyond it is refused);
//   - the source really holds a key for it (the AIVPN server has the client and
//     the key parses).
//
// A device id scopes the credential: the key is then issued for (login, device)
// via Source.KeyForDevice, so two installs never share an AIVPN client. Without
// one the legacy login-wide client is used, byte-for-byte as before.
//
// Any failure is a plain "no key": callers must not tell the client which gate
// failed. Both /info and GET /aivpn/<token> go through this one function so the
// two surfaces can never drift apart.
func (s *Server) aivpnKey(r *http.Request, c *store.Customer) (string, aivpnOutcome) {
	src := s.cfg.AIVPN
	if src == nil || !src.Enabled() || c == nil || !c.Active() {
		return "", aivpnUnavailable
	}
	vc := appVersionCode(r.UserAgent())
	if vc <= 0 || vc < src.MinVersionCode() {
		return "", aivpnUnavailable
	}
	if !src.Allowed(c.Login) {
		return "", aivpnUnavailable
	}
	dev := deviceID(r)
	if !deviceIDRe.MatchString(dev) {
		// Same rule as /sub: a malformed id is "no id" — it never blocks a
		// customer and never scopes a credential.
		dev = ""
	}
	if dev != "" {
		// Checked after the version/whitelist gates on purpose: a request that
		// can never receive a key (an old build polling in a loop, a login off
		// the list) must not consume a device slot.
		if admitted, _ := s.deviceAdmitted(c.Login, dev); !admitted {
			return "", aivpnDeviceLimit
		}
	}
	var (
		key string
		err error
	)
	if dev != "" {
		key, err = src.KeyForDevice(r.Context(), c.Login, dev)
	} else {
		key, err = src.Key(r.Context(), c.Login)
	}
	if err != nil || key == "" {
		return "", aivpnUnavailable
	}
	return key, aivpnOK
}

// handleAivpn serves GET /aivpn/<token>: the subscriber's AIVPN connection key.
//
// The token is the same per-customer secret /sub uses. The response is a tiny
// JSON object ({"key": "aivpn://…"}) rather than a profile: the key is pasted
// into the app's own AIVPN VpnService and has nothing to do with sing-box. The
// key is a live credential, hence Cache-Control: no-store and the hard gates in
// aivpnKey. A ?device= (or X-Device-Id) picks that device's own credential.
func (s *Server) handleAivpn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	tok := strings.TrimPrefix(r.URL.Path, "/aivpn/")
	if tok == "" || strings.Contains(tok, "/") {
		http.NotFound(w, r)
		return
	}
	c, err := s.st.ByToken(tok)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !c.Active() {
		// Same shape as /sub: the customer is told the account is over, and gets
		// no credential.
		http.Error(w, "subscription expired", http.StatusPaymentRequired)
		return
	}
	key, res := s.aivpnKey(r, c)
	switch res {
	case aivpnDeviceLimit:
		// Exactly what /sub answers a NEW device over the cap: the customer
		// already knows the cap exists, so naming it here reveals nothing new.
		http.Error(w, "device limit reached", http.StatusForbidden)
		return
	case aivpnUnavailable:
		// ONE answer for every other gate failure (source off for this client,
		// version too old, login not whitelisted, no key on the server). A
		// finer-grained body would let a token holder probe the gate
		// configuration.
		http.Error(w, "aivpn unavailable", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"key": key})
}
