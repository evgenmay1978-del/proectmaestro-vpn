package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
	"os"
	"strconv"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

const flatCDNSubscriptionPrefix = "/cdn-sub/"

// flatCDNMaxPageSize is the largest customer page the control plane accepts;
// a bigger limit is answered with a forbidden error, not an empty page.
const flatCDNMaxPageSize = 200

var (
	errFlatCDNUnavailable       = errors.New("flat cdn subscription unavailable")
	errFlatCDNUnsupportedFormat = errors.New("unsupported flat cdn subscription format")
)

// flatCDNCustomerByLogin is the frozen-port lookup used to read the paid term of
// a customer row without widening the Business contract.
type flatCDNCustomerByLogin interface {
	CustomerByLogin(context.Context, string) (CustomerView, error)
}

// flatCDNEntitlement is the product state behind the standalone CDN
// subscription: whether the bearer is a known customer, whether the regular VPN
// subscription is active, and how many CDN bytes are still available.
// Entitled reports whether the CDN may be served right now: the regular
// subscription is active AND has not expired yet. The expiry is checked
// explicitly because the commercial view can stay "active" while a billing
// period (not the paid term) still runs.
func (e flatCDNEntitlement) Entitled() bool {
	return e.Known && e.Active && (e.ExpiresAtUnix == 0 || e.ExpiresAtUnix > time.Now().Unix())
}

type flatCDNEntitlement struct {
	Known            bool
	Login            string
	CustomerID       string
	ExpiresAtUnix    int64
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
	if !entitlement.Entitled() {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "vpn subscription inactive"})
		return
	}
	if entitlement.AvailableBytes <= 0 {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "cdn traffic exhausted"})
		return
	}
	body, contentType, err := s.renderFlatCDNSubscription(r.Context(), entitlement, token, r.URL.Query())
	if err != nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("MaestroVPN VPN + CDN")))
	w.Header().Set("Profile-Update-Interval", "1")
	// The white-list balance IS the remaining quota of this subscription, so it
	// is reported as an untouched total and every client shows it as remaining.
	expire := entitlement.PeriodEndsAtUnix
	if entitlement.ExpiresAtUnix > 0 && (expire == 0 || entitlement.ExpiresAtUnix < expire) {
		// The CDN cannot outlive the regular subscription, so the client shows the
		// earlier of the two ends instead of a far-away billing period.
		expire = entitlement.ExpiresAtUnix
	}
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=0; total=%d; expire=%d", entitlement.AvailableBytes, expire))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// renderFlatCDNSubscription renders the paid flat-CDN node for the requested
// client representation. The on-disk node file wins; FlatCDNSubFile stays as a
// verbatim emergency override that ignores the requested format.
func (s *ControlPlaneServer) renderFlatCDNSubscription(_ context.Context, entitlement flatCDNEntitlement, token string, query url.Values) ([]byte, string, error) {
	raw := query.Get("raw") == "1"
	nodes, nodeErr := s.flatCDNNodes()
	if nodeErr == nil {
		if uuid := flatCDNCustomerUUID(s.cfg.FlatCDNUUIDSecret, entitlement.CustomerID); uuid != "" {
			// One credential per customer: the flat origin can meter and revoke
			// every subscriber on its own.
			for index := range nodes {
				nodes[index].ClientID = uuid
			}
		}
		if raw {
			link, err := subgen.WhiteListShareLink(nodes[0])
			if err != nil {
				return nil, "", err
			}
			return []byte(link), "text/plain; charset=utf-8", nil
		}
		switch strings.ToLower(strings.TrimSpace(query.Get("format"))) {
		case "", "xray", "json":
			// The CDN subscription carries CDN nodes only: the ordinary servers live
			// in the regular subscription, and the product keeps them separate.
			// Each node is rendered on its own and merged, so one rejected node cannot
			// hide the others and every remark keeps its own country label.
			rendered, err := flatCDNXrayDocument(nodes)
			if err != nil {
				return nil, "", err
			}
			return rendered, "application/json; charset=utf-8", nil
		case "links", "link", "base64", "v2ray", "v2raytun", "mihomo", "clash":
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

// ordinarySubscriptionPayload returns the customer's regular subscription in the
// share-link representation, which is the common denominator the subgen
// combiners accept. An empty answer simply means "CDN node only".
func (s *ControlPlaneServer) ordinarySubscriptionPayload(ctx context.Context, token string) string {
	source, ok := s.business.(requestSubscriptionSource)
	if !ok || strings.TrimSpace(token) == "" {
		return ""
	}
	snapshot, err := source.subscriptionSnapshotForRequest(ctx, token, subscriptionRenderOptions{
		ClientRequest: true, Links: true, Endpoint: subscriptionEndpointBase,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(snapshot.Document))
}


// handleControlPlaneFlatCDNCharge charges already-metered bytes to the customer
// that owns the reported credential. The origin meter sends only the credential
// and a byte delta; entitlement and balances stay owned by the control plane.
func (s *ControlPlaneServer) handleControlPlaneFlatCDNCharge(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	var request struct {
		UUID           string `json:"uuid"`
		Bytes          int64  `json:"bytes"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	uuid := strings.ToLower(strings.TrimSpace(request.UUID))
	if len(uuid) != 36 || request.Bytes <= 0 {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	business, ok := s.business.(panelWhiteListBusiness)
	if !ok || s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	key := strings.TrimSpace(request.IdempotencyKey)
	if key == "" {
		key = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if key == "" {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "idempotency key required"})
		return
	}
	login, err := s.flatCDNLoginByUUID(r.Context(), uuid)
	if err != nil {
		log.Printf("flat-cdn charge lookup failed uuid=%s: %v", uuid, err)
		writeControlPlaneBusinessError(w, err)
		return
	}
	if login == "" {
		writeControlPlaneJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	view, err := business.PanelWhiteListAdmin(r.Context(), panelWhiteListCommand{
		Login: login, Action: "debit", Bytes: request.Bytes, Actor: "flatmeter", IdempotencyKey: key,
	})
	if err != nil {
		log.Printf("flat-cdn charge rejected login=%s uuid=%s bytes=%d: %v", login, uuid, request.Bytes, err)
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"login": login, "bytes": request.Bytes, "remaining_bytes": view.RemainingBytes})
}

// flatCDNLoginByUUID maps an origin credential back to the customer login with
// the same deterministic derivation the subscription uses.
func (s *ControlPlaneServer) flatCDNLoginByUUID(ctx context.Context, uuid string) (string, error) {
	secret := strings.TrimSpace(s.cfg.FlatCDNUUIDSecret)
	if secret == "" {
		return "", serviceBusinessError{err: controlplane.ErrUnavailable, status: http.StatusServiceUnavailable}
	}
	active := true
	customers, err := s.business.ListCustomers(ctx, CustomerFilter{Active: &active, Limit: flatCDNMaxPageSize})
	if err != nil {
		return "", err
	}
	for _, customer := range customers {
		if flatCDNCustomerUUID(secret, customer.CustomerID) == uuid {
			return customer.Login, nil
		}
	}
	return "", nil
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
// flatCDNXrayDocument renders every node separately and merges the arrays.
func flatCDNXrayDocument(nodes []subgen.WhiteListNode) ([]byte, error) {
	merged := make([]json.RawMessage, 0, len(nodes))
	for _, node := range nodes {
		part, err := subgen.WhiteListXrayJSONSubscription(node, "")
		if err != nil {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(part, &items) != nil {
			continue
		}
		merged = append(merged, items...)
	}
	if len(merged) == 0 {
		return nil, errFlatCDNUnavailable
	}
	return json.Marshal(merged)
}

func flatCDNShareLinkPayload(nodes []subgen.WhiteListNode) ([]byte, error) {
	links := make([]string, 0, len(nodes))
	for _, node := range nodes {
		link, err := subgen.WhiteListShareLink(node)
		if err != nil {
			return nil, err
		}
		if label := strings.TrimSpace(node.Label); label != "" {
			// The subgen share link keeps one fixed label; the panel gives every
			// CDN node its own country label so clients can tell them apart.
			if base, _, found := strings.Cut(link, "#"); found {
				link = base
			}
			link += "#" + url.QueryEscape(label)
		}
		links = append(links, link)
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))), nil
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
	if !entitlement.Entitled() {
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
	entitlement := flatCDNEntitlement{
		Known: true, Login: customer.Login, CustomerID: customer.CustomerID,
		Active: customer.Active, ExpiresAtUnix: customer.Expires.Unix(),
	}
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
	if source, ok := s.business.(flatCDNCustomerByLogin); ok && customer.Login != "" {
		// The paid term lives on the customer row, not in the billing period: an
		// expired subscription must switch the CDN off even while a period runs.
		if view, lookupErr := source.CustomerByLogin(ctx, customer.Login); lookupErr == nil {
			entitlement.ExpiresAtUnix = view.Expires.Unix()
			if !view.Expires.IsZero() && !view.Expires.After(time.Now()) {
				entitlement.Active = false
			}
		}
	}
	return entitlement, nil
}

// flatCDNCustomerUUID derives the stable VLESS credential of one customer from
// the panel secret. Deterministic (no extra state), unique per customer, and a
// valid canonical UUIDv4 so the subgen validators accept it.
func flatCDNCustomerUUID(secret, customerID string) string {
	secret = strings.TrimSpace(secret)
	customerID = strings.TrimSpace(customerID)
	if secret == "" || customerID == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("maestro-flat-cdn-uuid:" + customerID))
	sum := mac.Sum(nil)
	var id [16]byte
	copy(id[:], sum[:16])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

// flatCDNClientEntry is one origin credential the flat CDN agent installs.
type flatCDNClientEntry struct {
	UUID           string `json:"uuid"`
	Login          string `json:"login"`
	ExpiresAtUnix  int64  `json:"expires_at_unix"`
	AvailableBytes int64  `json:"available_bytes"`
}

// handleControlPlaneFlatCDNClients is the origin-side view of the entitlement:
// every customer whose regular subscription is active and whose CDN balance is
// positive, with the credential that must exist on the flat origin. The origin
// agent applies the diff and never decides entitlement itself.
func (s *ControlPlaneServer) handleControlPlaneFlatCDNClients(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if strings.TrimSpace(s.cfg.FlatCDNUUIDSecret) == "" || s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= flatCDNMaxPageSize {
			limit = parsed
		}
	}
	active := true
	customers, err := s.business.ListCustomers(r.Context(), CustomerFilter{Active: &active, Limit: limit})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	entries := make([]flatCDNClientEntry, 0, len(customers))
	for _, customer := range customers {
		uuid := flatCDNCustomerUUID(s.cfg.FlatCDNUUIDSecret, customer.CustomerID)
		if uuid == "" {
			continue
		}
		balance, balanceErr := s.commercial.WhiteListBalance(r.Context(), customer.CustomerID)
		if balanceErr != nil {
			// A customer without a white-list account has no CDN traffic at all.
			if controlPlaneCommercialStatus(balanceErr) == http.StatusNotFound {
				continue
			}
			writeControlPlaneCommercialError(w, balanceErr)
			return
		}
		if balance.AvailableBytes <= 0 {
			continue
		}
		entries = append(entries, flatCDNClientEntry{
			UUID: uuid, Login: customer.Login,
			ExpiresAtUnix: customer.Expires.Unix(), AvailableBytes: balance.AvailableBytes,
		})
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"clients": entries, "count": len(entries)})
}

func controlPlaneCommercialStatus(err error) int {
	type statusError interface{ HTTPStatus() int }
	var typed statusError
	if errors.As(err, &typed) {
		return typed.HTTPStatus()
	}
	return http.StatusConflict
}
