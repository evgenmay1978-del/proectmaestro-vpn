package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

const flatCDNSubscriptionPrefix = "/cdn-sub/"

var (
	errFlatCDNUnavailable       = errors.New("flat cdn subscription unavailable")
	errFlatCDNUnsupportedFormat = errors.New("unsupported flat cdn subscription format")
)

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
// The node itself lives in a root-only JSON file (a subgen.WhiteListNode), so the
// SAME generators that render the regular subscription also render every client
// representation here: full Xray JSON for Incy/Happ, base64 share links for
// third-party clients, native profiles for the app. What the panel owns is the
// ENTITLEMENT gate, matching the product rule:
//   - the regular VPN subscription must be ACTIVE (an expired VPN subscription
//     disables the CDN too, even when CDN gigabytes are still on the balance);
//   - the pre-paid CDN balance must be positive.
func (s *ControlPlaneServer) handleControlPlaneFlatCDNSubscription(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, flatCDNSubscriptionPrefix), "/")
	if token == "" || strings.Contains(token, "/") {
		writeControlPlaneJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
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
	body, contentType, err := s.renderFlatCDNSubscription(token, r.URL.Query())
	if err != nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("MaestroVPN CDN")))
	w.Header().Set("Profile-Update-Interval", "1")
	// The white-list balance IS the remaining quota of this subscription, so it
	// is reported as an untouched total and every client shows it as remaining.
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=0; total=%d; expire=%d", entitlement.AvailableBytes, entitlement.PeriodEndsAtUnix))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// renderFlatCDNSubscription renders the paid flat-CDN node for the requested
// client representation. The on-disk node file wins; FlatCDNSubFile stays as a
// verbatim emergency override that ignores the requested format.
func (s *ControlPlaneServer) renderFlatCDNSubscription(token string, query url.Values) ([]byte, string, error) {
	raw := query.Get("raw") == "1"
	nodes, nodeErr := s.flatCDNNodes()
	if nodeErr == nil {
		if raw {
			link, err := subgen.WhiteListShareLink(nodes[0])
			if err != nil {
				return nil, "", err
			}
			return []byte(link), "text/plain; charset=utf-8", nil
		}
		switch strings.ToLower(strings.TrimSpace(query.Get("format"))) {
		case "", "xray", "json":
			rendered, err := subgen.WhiteListXrayJSONSubscriptions(nodes)
			if err != nil {
				return nil, "", err
			}
			return rendered, "application/json; charset=utf-8", nil
		case "links", "link", "base64", "v2ray", "v2raytun", "mihomo", "clash":
			// A CDN-only subscription has no ordinary node, so the Mihomo
			// renderer (which keeps ordinary nodes selectable) cannot be used:
			// clients that consume share links get the links representation.
			rendered, err := flatCDNShareLinkPayload(nodes)
			if err != nil {
				return nil, "", err
			}
			return rendered, "text/plain; charset=utf-8", nil
		case "native":
			profiles, err := subgen.NativeWhiteListProfiles(nodes)
			if err != nil {
				return nil, "", err
			}
			rendered, err := json.Marshal(profiles)
			if err != nil {
				return nil, "", err
			}
			return rendered, "application/json; charset=utf-8", nil
		default:
			return nil, "", errFlatCDNUnsupportedFormat
		}
	}
	path := strings.TrimSpace(s.cfg.FlatCDNSubFile)
	if path == "" {
		return nil, "", errFlatCDNUnavailable
	}
	document, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(document)) == 0 {
		return nil, "", errFlatCDNUnavailable
	}
	body := renderFlatCDNPlaceholders(document, token)
	if raw {
		if decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body))); decodeErr == nil && len(decoded) != 0 {
			body = decoded
		}
	}
	return body, flatCDNContentType(body), nil
}

// flatCDNNodes reads the configured node file. A single object and an array of
// objects are both accepted.
func (s *ControlPlaneServer) flatCDNNodes() ([]subgen.WhiteListNode, error) {
	path := strings.TrimSpace(s.cfg.FlatCDNNodeFile)
	if path == "" {
		return nil, errFlatCDNUnavailable
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errFlatCDNUnavailable
	}
	var nodes []subgen.WhiteListNode
	if jsonErr := json.Unmarshal(raw, &nodes); jsonErr != nil {
		var node subgen.WhiteListNode
		if singleErr := json.Unmarshal(raw, &node); singleErr != nil {
			return nil, errFlatCDNUnavailable
		}
		nodes = []subgen.WhiteListNode{node}
	}
	if len(nodes) == 0 || len(nodes) > 16 {
		return nil, errFlatCDNUnavailable
	}
	return nodes, nil
}

// flatCDNShareLinkPayload is the encoded share-link subscription every
// third-party client understands.
func flatCDNShareLinkPayload(nodes []subgen.WhiteListNode) ([]byte, error) {
	links := make([]string, 0, len(nodes))
	for _, node := range nodes {
		link, err := subgen.WhiteListShareLink(node)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "
")))), nil
}

// writeFlatCDNSubscriptionDelivery answers the per-application delivery request
// for the standalone CDN subscription: the same descriptor shapes the regular
// subscription uses (Incy one-tap, Happ HTTPS+QR, Karing install-config), built
// from the public CDN subscription URL instead of the regular one.
func (s *ControlPlaneServer) writeFlatCDNSubscriptionDelivery(w http.ResponseWriter, r *http.Request, client string) {
	token := controlPlaneBearerToken(r)
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
	delivery, err := subgen.BuildDelivery(strings.ToUpper(client), flatCDNSubscriptionURL(s.cfg.SubBaseURL, token))
	if err != nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, CommercialDeliveryView{
		Client: client, Format: delivery.Format, URL: delivery.URL, CopyURL: delivery.CopyURL,
	})
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

func renderFlatCDNPlaceholders(document []byte, token string) []byte {
	replacer := strings.NewReplacer(
		"{{TOKEN}}", token,
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
