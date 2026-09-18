package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const flatCDNSubscriptionPrefix = "/cdn-sub/"

// handleControlPlaneFlatCDNSubscription serves the standalone CDN subscription
// — the second subscription a customer holds next to the regular VPN one.
//
// The node document is deliberately static (one origin, one credential) and is
// read from a root-only file so it can be rotated without a panel release. What
// the panel owns here is the ENTITLEMENT gate, matching the product rule:
//   - the regular VPN subscription must be ACTIVE (an expired VPN subscription
//     disables the CDN too, even when CDN gigabytes are still on the balance);
//   - the pre-paid CDN balance must be positive.
//
// Because every customer resolves to the same on-disk template, {{TOKEN}},
// {{LOGIN}} and {{CDN_SUB_URL}} placeholders are substituted per request.
func (s *ControlPlaneServer) handleControlPlaneFlatCDNSubscription(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSpace(s.cfg.FlatCDNSubFile)
	if path == "" || s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, flatCDNSubscriptionPrefix), "/")
	if token == "" || strings.Contains(token, "/") {
		writeControlPlaneJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	document, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(document)) == 0 {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	customer, err := s.commercial.CustomerByToken(r.Context(), token)
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	if !customer.Active {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "vpn subscription inactive"})
		return
	}
	balance, err := s.commercial.WhiteListBalance(r.Context(), customer.CustomerID)
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	if balance.AccountID != "" && balance.AccountID != customer.CustomerID {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if balance.AvailableBytes <= 0 {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "cdn traffic exhausted"})
		return
	}
	body := renderFlatCDNSubscription(document, token, customer.Login)
	if r.URL.Query().Get("raw") == "1" {
		if decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body))); decodeErr == nil && len(decoded) != 0 {
			body = decoded
		}
	}
	w.Header().Set("Content-Type", flatCDNContentType(body))
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("MaestroVPN CDN")))
	w.Header().Set("Profile-Update-Interval", "1")
	// The white-list balance IS the remaining quota of this subscription, so it
	// is reported as an untouched total and every client shows it as remaining.
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=0; total=%d; expire=%d", balance.AvailableBytes, balance.PeriodEndsAtUnix))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func renderFlatCDNSubscription(document []byte, token, login string) []byte {
	replacer := strings.NewReplacer(
		"{{TOKEN}}", token,
		"{{LOGIN}}", login,
		"{{CDN_SUB_URL}}", flatCDNSubscriptionPrefix+token,
	)
	return []byte(replacer.Replace(string(document)))
}

func flatCDNContentType(document []byte) string {
	trimmed := bytes.TrimSpace(document)
	if len(trimmed) != 0 && trimmed[0] == '{' {
		return "application/json; charset=utf-8"
	}
	return "text/plain; charset=utf-8"
}
