package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func requireControlPlaneMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeControlPlaneJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	return false
}

func writeControlPlaneBusinessError(w http.ResponseWriter, err error) {
	type statusError interface{ HTTPStatus() int }
	if typed, ok := err.(statusError); ok {
		writeControlPlaneJSON(w, typed.HTTPStatus(), map[string]string{"error": err.Error()})
		return
	}
	writeControlPlaneJSON(w, http.StatusConflict, map[string]string{"error": "request rejected"})
}

func (s *ControlPlaneServer) handleControlPlaneTariffs(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	tariffs, err := s.business.Tariffs(r.Context())
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{
		"tariffs": tariffs, "sbp_phone": s.cfg.SBPPhone, "pay_url": s.cfg.PayURL,
	})
}

func (s *ControlPlaneServer) handleControlPlaneCreateOrder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Tariff string `json:"tariff"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	order, err := s.business.CreateOrder(r.Context(), CreateOrderCommand{
		Tariff: request.Tariff, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	if order.SBPPhone == "" {
		order.SBPPhone = s.cfg.SBPPhone
	}
	if order.PayURL == "" {
		order.PayURL = s.cfg.PayURL
	}
	writeControlPlaneJSON(w, http.StatusOK, order)
}

func (s *ControlPlaneServer) handleControlPlaneOrder(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/order/")
	if id == "" || strings.Contains(id, "/") {
		s.controlPlaneNotFound(w, r)
		return
	}
	order, err := s.business.OrderByID(r.Context(), id)
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, order)
}

func (s *ControlPlaneServer) handleControlPlanePaymentClaim(w http.ResponseWriter, r *http.Request) {
	var request struct {
		OrderID string `json:"order_id"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	order, err := s.business.MarkPaymentClaimed(r.Context(), ClaimPaymentCommand{
		OrderID: request.OrderID, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, order)
}

func (s *ControlPlaneServer) handleControlPlaneTrial(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Nick   string `json:"nick"`
		Anchor string `json:"anchor"`
		Device string `json:"device"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	nick := strings.TrimSpace(request.Nick)
	if !claimCodeRe.MatchString(nick) || strings.TrimSpace(request.Anchor) == "" {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid trial identity"})
		return
	}
	view, err := s.business.RedeemTrial(r.Context(), RedeemTrialCommand{
		Login: "trial-" + nick, Anchor: request.Anchor, DRMIdentity: request.Device,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneClaim(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code   string `json:"code"`
		Device string `json:"device"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	code := strings.TrimSpace(request.Code)
	if !claimCodeRe.MatchString(code) {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid code"})
		return
	}
	customer, err := s.business.CustomerByLogin(r.Context(), code)
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	decision, err := s.business.TouchDevice(r.Context(), TouchDeviceCommand{
		Login: customer.Login, DeviceID: strings.TrimSpace(request.Device),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	if !decision.Allowed {
		writeControlPlaneJSON(w, http.StatusTooManyRequests, map[string]string{"error": "device limit reached"})
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, customer)
}

func (s *ControlPlaneServer) handleControlPlaneSub(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	rest := strings.TrimPrefix(r.URL.Path, "/sub/")
	info := strings.HasSuffix(rest, "/info")
	helpers := strings.HasSuffix(rest, "/helpers")
	if info {
		rest = strings.TrimSuffix(rest, "/info")
	}
	if helpers {
		rest = strings.TrimSuffix(rest, "/helpers")
	}
	if rest == "" || strings.Contains(rest, "/") {
		s.controlPlaneNotFound(w, r)
		return
	}
	var snapshot SubscriptionSnapshot
	var err error
	if source, ok := s.business.(requestSubscriptionSource); ok && !info && !helpers {
		query := r.URL.Query()
		snapshot, err = source.subscriptionSnapshotForRequest(r.Context(), rest, subscriptionRenderOptions{
			ClientRequest: true, UserAgent: r.UserAgent(),
			Links: query.Get("app") == "karing" || query.Get("format") == "links",
		})
	} else {
		snapshot, err = s.business.SubscriptionSnapshot(r.Context(), rest)
	}
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	if info {
		writeControlPlaneSubInfo(w, snapshot.Customer)
		return
	}
	if !snapshot.Customer.Active || !snapshot.Customer.Expires.After(time.Now()) {
		http.Error(w, "subscription expired", http.StatusPaymentRequired)
		return
	}
	if helpers {
		writeControlPlaneJSON(w, http.StatusOK, map[string]any{})
		return
	}
	contentType := snapshot.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(snapshot.Document)
}

func writeControlPlaneSubInfo(w http.ResponseWriter, customer CustomerView) {
	untilExpiry := time.Until(customer.Expires)
	daysLeft := int(untilExpiry / (24 * time.Hour))
	if untilExpiry > 0 && untilExpiry%(24*time.Hour) != 0 {
		daysLeft++
	}
	if daysLeft < 0 {
		daysLeft = 0
	}
	active := customer.Active && untilExpiry > 0
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{
		"login":     customer.Login,
		"expires":   customer.Expires,
		"days_left": daysLeft,
		"active":    active,
	})
}

func (s *ControlPlaneServer) handleControlPlaneOTA(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	manifest, err := s.business.ApprovedOTA(r.Context())
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, manifest)
}

func (s *ControlPlaneServer) handleControlPlaneProvision(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Login string `json:"login"`
		Days  int    `json:"days"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	view, err := s.business.ProvisionCustomer(r.Context(), ProvisionCustomerCommand{
		Login: request.Login, Days: request.Days, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneSetExpiry(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Login   string `json:"login"`
		Expires string `json:"expires"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	expires, err := time.Parse(time.RFC3339, request.Expires)
	if err != nil {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "expires must be RFC3339"})
		return
	}
	view, err := s.business.SetCustomerExpiry(r.Context(), SetExpiryCommand{
		Login: request.Login, Expires: expires, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneResetDevices(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Login string `json:"login"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	err := s.business.ResetDevices(r.Context(), ResetDevicesCommand{
		Login: request.Login, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *ControlPlaneServer) handleControlPlaneCustomer(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	view, err := s.business.CustomerByLogin(r.Context(), r.URL.Query().Get("login"))
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) controlPlaneReconcile(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Login string `json:"login"`
		}
		if !decodeControlPlaneMutation(w, r, &request) {
			return
		}
		view, err := s.business.ReconcileServices(r.Context(), ReconcileServicesCommand{
			Login: request.Login, Service: service, IdempotencyKey: r.Header.Get("Idempotency-Key"),
		})
		if err != nil {
			writeControlPlaneBusinessError(w, err)
			return
		}
		writeControlPlaneJSON(w, http.StatusOK, view)
	}
}

func (s *ControlPlaneServer) handleControlPlaneMigrate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Endpoint string `json:"endpoint"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	view, err := s.business.MigrateServiceEndpoint(r.Context(), MigrateEndpointCommand{
		Service: "anytls", Endpoint: request.Endpoint, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneConfirmOrder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		OrderID string `json:"order_id"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	view, err := s.business.ConfirmPayment(r.Context(), ConfirmPaymentCommand{
		OrderID: request.OrderID, Actor: "admin", IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneCancelOrder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		OrderID string `json:"order_id"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	view, err := s.business.CancelOrder(r.Context(), CancelOrderCommand{
		OrderID: request.OrderID, Actor: "admin", IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneOLCRTC(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	view, err := s.business.OLCRTCState(r.Context())
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneOLCRTCRoom(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Login           string `json:"login"`
		Room            string `json:"room"`
		Provider        string `json:"provider"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	view, err := s.business.SetOLCRTCRoom(r.Context(), SetOLCRTCRoomCommand{
		Login: request.Login, Room: request.Room, Provider: request.Provider,
		ExpectedVersion: request.ExpectedVersion, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func decodeControlPlaneBody(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return false
	}
	return true
}
