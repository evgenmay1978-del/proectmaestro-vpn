package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed customer_cabinet.html
var customerCabinetHTML []byte

// This optional read port does not change existing commercial adapters.
type commercialOrderReader interface {
	CommercialOrder(context.Context, string) (CommercialOrderView, error)
}

func (b *ServiceBusiness) CommercialOrder(ctx context.Context, orderID string) (CommercialOrderView, error) {
	binding, err := b.CommercialOrderBinding(ctx, orderID)
	if err != nil {
		return CommercialOrderView{}, err
	}
	order, err := b.commercialOrderFromBusiness(ctx, binding.AccountID, orderID)
	if err != nil {
		return CommercialOrderView{}, err
	}
	customer, err := b.service.BusinessCustomerByID(ctx, binding.AccountID)
	if err != nil {
		return CommercialOrderView{}, businessError(err)
	}
	order.Login = customer.Login
	return order, nil
}

func (s *ControlPlaneServer) handleControlPlaneCustomerCabinet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	// A personal API must not accept another site's browser request.
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != r.Host || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
	}
	var ok bool
	if r, ok = customerCabinetCommandRequest(w, r); !ok {
		return
	}
	switch r.URL.Path {
	case "/cabinet/":
		if !requireControlPlaneMethod(w, r, http.MethodGet) {
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(customerCabinetHTML)
	case "/cabinet/api/config":
		if requireControlPlaneMethod(w, r, http.MethodGet) {
			writeControlPlaneJSON(w, http.StatusOK, map[string]bool{"cdn_sales_enabled": s.cfg.CustomerCDNSalesEnabled})
		}
	case "/cabinet/api/claim":
		s.handleCustomerCabinetLogin(w, r)
	case "/cabinet/api/profile":
		s.handleControlPlaneCommercialProfile(w, r)
	case "/cabinet/api/balance":
		s.handleControlPlaneCommercialBalance(w, r)
	case "/cabinet/api/runtime":
		s.handleControlPlaneWhiteListNativeRuntime(w, r)
	case "/cabinet/api/catalog":
		s.handleControlPlaneCommercialCatalog(w, r)
	case "/cabinet/api/tariffs":
		s.handleControlPlaneTariffs(w, r)
	case "/cabinet/api/delivery":
		s.handleControlPlaneCommercialDelivery(w, r)
	case "/cabinet/api/order":
		s.handleCustomerCabinetCreateOrder(w, r)
	default:
		if rest, ok := strings.CutPrefix(r.URL.Path, "/cabinet/api/order/"); ok {
			parts := strings.Split(rest, "/")
			if len(parts) == 1 && parts[0] != "" {
				s.handleCustomerCabinetOrder(w, r, parts[0])
				return
			}
			if len(parts) == 2 && parts[0] != "" && parts[1] == "paid-claim" {
				// Verify the family before reusing the existing claim handler.
				if _, ok := s.customerCabinetOrderBinding(w, r, parts[0]); ok {
					s.handleControlPlaneCommercialPaidClaim(w, r, parts[0])
				}
				return
			}
		}
		s.controlPlaneNotFound(w, r)
	}
}

// The configured CDN currently forwards GET/HEAD/OPTIONS only. A same-origin
// browser can carry these three authenticated mutations in an explicit header,
// without putting a login or subscription token in the URL. An ordinary GET
// never mutates anything, and the existing POST handlers retain all guards.
func customerCabinetCommandRequest(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	values := r.Header.Values("X-Maestro-Command")
	if len(values) == 0 {
		return r, true
	}
	allowed := r.URL.Path == "/cabinet/api/claim" || r.URL.Path == "/cabinet/api/order"
	if rest, ok := strings.CutPrefix(r.URL.Path, "/cabinet/api/order/"); ok {
		parts := strings.Split(rest, "/")
		allowed = len(parts) == 2 && parts[0] != "" && parts[1] == "paid-claim"
	}
	reject := func() (*http.Request, bool) {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cabinet command"})
		return nil, false
	}
	if !allowed || r.Method != http.MethodGet || len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 4096 ||
		r.ContentLength > 0 || len(r.TransferEncoding) != 0 || strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		return reject()
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
	if err != nil || len(raw) > 4096 {
		return reject()
	}
	var command struct {
		Method string          `json:"method"`
		Body   json.RawMessage `json:"body"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&command) != nil || command.Method != http.MethodPost || len(command.Body) == 0 ||
		decoder.Decode(new(any)) != io.EOF {
		return reject()
	}
	restored := r.Clone(r.Context())
	restored.Method = http.MethodPost
	restored.Body = io.NopCloser(bytes.NewReader(command.Body))
	restored.ContentLength = int64(len(command.Body))
	restored.Header.Del("X-Maestro-Command")
	restored.Header.Set("Content-Type", "application/json")
	return restored, true
}

func (s *ControlPlaneServer) handleCustomerCabinetLogin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code string `json:"code"`
	}
	if !decodeControlPlanePublicMutation(w, r, &request) {
		return
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	if !s.controlPlanePanelRateLimit(w, r, "cabinet.login.peer", peer, 600, time.Minute, time.Minute) {
		return
	}
	code := strings.TrimSpace(request.Code)
	loginHash := sha256.Sum256([]byte(code))
	if !s.controlPlanePanelRateLimit(w, r, "cabinet.login.code", hex.EncodeToString(loginHash[:]), 10, 10*time.Minute, 15*time.Minute) {
		return
	}
	if !claimCodeRe.MatchString(code) {
		writeControlPlaneJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
		return
	}
	customer, err := s.business.CustomerByLogin(r.Context(), code)
	if err != nil {
		writeControlPlaneJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
		return
	}
	// A browser cabinet is not another VPN device. The existing login credential
	// authenticates the customer without claiming an additional device slot.
	writeControlPlaneJSON(w, http.StatusOK, customer)
}

func (s *ControlPlaneServer) handleCustomerCabinetCreateOrder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ProductID string `json:"product_id"`
	}
	if !decodeControlPlanePublicMutation(w, r, &request) {
		return
	}
	if !s.cfg.CustomerCDNSalesEnabled {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cdn sales closed"})
		return
	}
	if _, ok := s.controlPlaneCommercialCustomer(w, r); !ok {
		return
	}
	if strings.TrimSpace(request.ProductID) == "" {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid product"})
		return
	}
	s.handleControlPlaneCreateCommercialOrder(w, r, request.ProductID, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

func (s *ControlPlaneServer) customerCabinetOrderBinding(w http.ResponseWriter, r *http.Request, orderID string) (CommercialOrderBindingView, bool) {
	customer, ok := s.controlPlaneCommercialCustomer(w, r)
	if !ok {
		return CommercialOrderBindingView{}, false
	}
	binding, err := s.commercial.CommercialOrderBinding(r.Context(), orderID)
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return CommercialOrderBindingView{}, false
	}
	if binding.AccountID != customer.CustomerID || binding.Family != CommercialOrderFamilyWhiteListTopUp {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return CommercialOrderBindingView{}, false
	}
	return binding, true
}

func (s *ControlPlaneServer) handleCustomerCabinetOrder(w http.ResponseWriter, r *http.Request, orderID string) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := s.customerCabinetOrderBinding(w, r, orderID); !ok {
		return
	}
	reader, ok := s.commercial.(commercialOrderReader)
	if !ok {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	order, err := reader.CommercialOrder(r.Context(), orderID)
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, order)
}

// Called only after the panel's existing session, CSRF and payment permission guards.
func (s *ControlPlaneServer) controlPlanePanelCommercialDecision(w http.ResponseWriter, r *http.Request, orderID, actor string, confirm bool) bool {
	if s.commercial == nil {
		return false
	}
	binding, err := s.commercial.CommercialOrderBinding(r.Context(), orderID)
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return true
	}
	if binding.Family != CommercialOrderFamilyWhiteListTopUp {
		return false
	}
	command := CommercialOrderDecisionCommand{OrderID: orderID, Actor: actor, IdempotencyKey: r.Header.Get("Idempotency-Key")}
	var order CommercialOrderView
	if confirm {
		order, err = s.commercial.ConfirmCommercialOrder(r.Context(), command)
	} else {
		order, err = s.commercial.RejectCommercialOrder(r.Context(), command)
	}
	if err != nil {
		writeControlPlaneCommercialError(w, err)
	} else {
		writeControlPlaneJSON(w, http.StatusOK, map[string]any{"order": order, "family": CommercialOrderFamilyWhiteListTopUp})
	}
	return true
}

func (s *ControlPlaneServer) controlPlanePanelCommercialOrders(w http.ResponseWriter, r *http.Request, orders []OrderView, nextCursor string) {
	reader, ok := s.commercial.(commercialOrderReader)
	if !ok || s.commercial == nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	catalog, err := s.commercial.CommercialCatalog(r.Context())
	if err != nil {
		writeControlPlaneCommercialError(w, err)
		return
	}
	productIDs := make(map[string]bool, len(catalog.Products))
	for _, product := range catalog.Products {
		productIDs[product.ID] = true
	}
	result := make([]CommercialOrderView, 0)
	for _, order := range orders {
		if !productIDs[order.Tariff] {
			continue
		}
		view, err := reader.CommercialOrder(r.Context(), order.OrderID)
		if err != nil {
			writeControlPlaneCommercialError(w, err)
			return
		}
		result = append(result, view)
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"orders": result, "next_cursor": nextCursor})
}
