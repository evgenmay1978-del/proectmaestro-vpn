package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const flatCDNSubscriptionPrefix = "/cdn-sub/"

// flatCDNEntitlement is the product state behind the standalone CDN
// subscription: whether the bearer is a known customer, whether the regular VPN
// subscription is active, and how many CDN bytes are still available.
type flatCDNEntitlement struct {
	Known            bool
	Login            string
	Active           bool
	AvailableBytes   int64
	PeriodEndsAtUnix int64
}

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
	entitlement, err := s.flatCDNEntitlement(r.Context(), token)
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	if !entitlement.Known {
		writeControlPlaneJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !entitlement.Active {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "vpn subscription inactive"})
		return
	}
	if entitlement.AvailableBytes <= 0 {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "cdn traffic exhausted"})
		return
	}
	body := renderFlatCDNSubscription(document, token, entitlement.Login)
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
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=0; total=%d; expire=%d", entitlement.AvailableBytes, entitlement.PeriodEndsAtUnix))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// flatCDNSubscriptionURL builds the public second-subscription URL a customer
// imports into the client next to the regular subscription.
func flatCDNSubscriptionURL(base, token string) string {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	token = strings.TrimSpace(token)
	if base == "" || token == "" || strings.Contains(token, "/") {
		return ""
	}
	return base + flatCDNSubscriptionPrefix + token
}

// controlPlaneBearerToken returns the customer bearer carried by the request.
func controlPlaneBearerToken(r *http.Request) string {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
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

// flatCDNEntitlement resolves the gate for one bearer: identity, whether the
// regular VPN subscription is still active, and the remaining CDN bytes.
//
// The balance lookup only knows customers that already bought CDN traffic: a
// customer who never topped up has no white-list account and the lookup returns
// "not found". That is a zero balance, not a missing or broken subscription, so
// it is reported as zero and the customer still gets the entitlement message.
// Unknown bearers keep the lookup error.
func (s *ControlPlaneServer) flatCDNEntitlement(ctx context.Context, token string) (flatCDNEntitlement, error) {
	if s.commercial == nil {
		return flatCDNEntitlement{}, serviceBusinessError{err: controlplane.ErrUnavailable, status: http.StatusServiceUnavailable}
	}
	customer, err := s.commercial.CustomerByToken(ctx, token)
	if err != nil {
		return flatCDNEntitlement{}, err
	}
	entitlement := flatCDNEntitlement{Known: true, Login: customer.Login, Active: customer.Active}
	balance, balanceErr := s.commercial.WhiteListBalance(ctx, customer.CustomerID)
	if balanceErr != nil {
		// A customer who never bought CDN traffic has no white-list account yet.
		// That is a zero balance, not a missing or broken subscription.
		if controlPlaneCommercialStatus(balanceErr) == http.StatusNotFound {
			return entitlement, nil
		}
		return flatCDNEntitlement{}, balanceErr
	}
	if balance.AccountID != "" && balance.AccountID != customer.CustomerID {
		return flatCDNEntitlement{}, serviceBusinessError{err: controlplane.ErrForbidden, status: http.StatusForbidden}
	}
	entitlement.AvailableBytes = balance.AvailableBytes
	entitlement.PeriodEndsAtUnix = balance.PeriodEndsAtUnix
	return entitlement, nil
}

func controlPlaneCommercialStatus(err error) int {
	type statusError interface{ HTTPStatus() int }
	var typed statusError
	if errors.As(err, &typed) {
		return typed.HTTPStatus()
	}
	return http.StatusConflict
}
