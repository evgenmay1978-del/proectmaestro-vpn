package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const (
	controlPlanePanelCookie     = "mp_session"
	controlPlanePanelCSRFCookie = "mp_csrf"
)

type panelCustomerView struct {
	Login      string     `json:"login"`
	Expires    time.Time  `json:"expires"`
	DaysLeft   int        `json:"days_left"`
	Active     bool       `json:"active"`
	Disabled   bool       `json:"disabled,omitempty"`
	Devices    int        `json:"devices"`
	Protocols  []string   `json:"protocols"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`
	Generation int64      `json:"generation,omitempty"`
}

type panelOrderView struct {
	OrderID           string `json:"order_id"`
	Code              string `json:"code"`
	RUB               int    `json:"rub"`
	Days              int    `json:"days,omitempty"`
	Tariff            string `json:"tariff,omitempty"`
	SBPPhone          string `json:"sbp_phone,omitempty"`
	PayURL            string `json:"pay_url,omitempty"`
	Status            string `json:"status"`
	PaymentState      string `json:"payment_state,omitempty"`
	ProvisioningState string `json:"provisioning_state,omitempty"`
	ResultGeneration  int64  `json:"result_generation,omitempty"`
}

type panelConfirmPaymentResult struct {
	Order     panelOrderView    `json:"order"`
	Customer  panelCustomerView `json:"customer"`
	Operation OperationView     `json:"operation"`
}

func toPanelCustomer(view CustomerView) panelCustomerView {
	return panelCustomerView{
		Login: view.Login, Expires: view.Expires, DaysLeft: view.DaysLeft, Active: view.Active,
		Disabled: view.Disabled, Devices: view.Devices, Protocols: append([]string{}, view.Protocols...),
		LastSeen: view.LastSeen, Generation: view.Generation,
	}
}

func toPanelCustomers(views []CustomerView) []panelCustomerView {
	result := make([]panelCustomerView, 0, len(views))
	for _, view := range views {
		result = append(result, toPanelCustomer(view))
	}
	return result
}

func toPanelOrder(view OrderView) panelOrderView {
	return panelOrderView{
		OrderID: view.OrderID, Code: view.Code, RUB: view.RUB, Days: view.Days, Tariff: view.Tariff,
		SBPPhone: view.SBPPhone, PayURL: view.PayURL, Status: view.Status, PaymentState: view.PaymentState,
		ProvisioningState: view.ProvisioningState, ResultGeneration: view.ResultGeneration,
	}
}

func toPanelOrders(views []OrderView) []panelOrderView {
	result := make([]panelOrderView, 0, len(views))
	for _, view := range views {
		result = append(result, toPanelOrder(view))
	}
	return result
}

const (
	controlPlanePanelPageDefault = 50
	controlPlanePanelPageMax     = 200

	controlPlanePanelLoginIPLimit    = 5
	controlPlanePanelReadIPLimit     = 600
	controlPlanePanelReadActorLimit  = 600
	controlPlanePanelWriteIPLimit    = 60
	controlPlanePanelWriteActorLimit = 60

	controlPlanePanelCursorVersion      = byte(1)
	controlPlanePanelCursorMaxEncoded   = 768
	controlPlanePanelCursorMaxEnvelope  = 512
	controlPlanePanelCursorMaxPlaintext = 384
	controlPlanePanelCursorAADPrefix    = "maestrovpn-panel-cursor-v1:"
)

type controlPlanePanelCursor struct {
	Kind   string `json:"k"`
	Key    string `json:"p,omitempty"`
	ID     string `json:"i"`
	Unix   int64  `json:"u,omitempty"`
	Filter string `json:"f,omitempty"`
}

func (s *ControlPlaneServer) controlPlanePanelCursorAEAD() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.panelCursorKey[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *ControlPlaneServer) controlPlanePanelPage(r *http.Request, kind string) (int, controlPlanePanelCursor, bool) {
	limit := controlPlanePanelPageDefault
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, controlPlanePanelCursor{}, false
		}
		limit = parsed
	}
	if limit > controlPlanePanelPageMax {
		limit = controlPlanePanelPageMax
	}
	rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if rawCursor == "" {
		return limit, controlPlanePanelCursor{Kind: kind}, true
	}
	if len(rawCursor) > controlPlanePanelCursorMaxEncoded {
		return 0, controlPlanePanelCursor{}, false
	}
	envelope, err := base64.RawURLEncoding.DecodeString(rawCursor)
	if err != nil || len(envelope) > controlPlanePanelCursorMaxEnvelope || len(envelope) < 2 || envelope[0] != controlPlanePanelCursorVersion {
		return 0, controlPlanePanelCursor{}, false
	}
	aead, err := s.controlPlanePanelCursorAEAD()
	if err != nil {
		return 0, controlPlanePanelCursor{}, false
	}
	minimum := 1 + aead.NonceSize() + aead.Overhead()
	if len(envelope) < minimum {
		return 0, controlPlanePanelCursor{}, false
	}
	nonceEnd := 1 + aead.NonceSize()
	plaintext, err := aead.Open(
		nil,
		envelope[1:nonceEnd],
		envelope[nonceEnd:],
		[]byte(controlPlanePanelCursorAADPrefix+kind),
	)
	if err != nil || len(plaintext) > controlPlanePanelCursorMaxPlaintext {
		return 0, controlPlanePanelCursor{}, false
	}
	var cursor controlPlanePanelCursor
	if json.Unmarshal(plaintext, &cursor) != nil || cursor.Kind != kind || strings.TrimSpace(cursor.ID) == "" {
		return 0, controlPlanePanelCursor{}, false
	}
	switch kind {
	case "customers":
		if strings.TrimSpace(cursor.Key) == "" || cursor.Unix != 0 || cursor.Filter != "" {
			return 0, controlPlanePanelCursor{}, false
		}
	case "orders", "audit":
		if cursor.Unix < 1 || cursor.Key != "" {
			return 0, controlPlanePanelCursor{}, false
		}
	default:
		return 0, controlPlanePanelCursor{}, false
	}
	return limit, cursor, true
}

func (s *ControlPlaneServer) encodeControlPlanePanelCursor(cursor controlPlanePanelCursor) (string, bool) {
	plaintext, err := json.Marshal(cursor)
	if err != nil || len(plaintext) > controlPlanePanelCursorMaxPlaintext {
		return "", false
	}
	aead, err := s.controlPlanePanelCursorAEAD()
	if err != nil {
		return "", false
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", false
	}
	ciphertext := aead.Seal(
		nil,
		nonce,
		plaintext,
		[]byte(controlPlanePanelCursorAADPrefix+cursor.Kind),
	)
	envelope := make([]byte, 1, 1+len(nonce)+len(ciphertext))
	envelope[0] = controlPlanePanelCursorVersion
	envelope = append(envelope, nonce...)
	envelope = append(envelope, ciphertext...)
	if len(envelope) > controlPlanePanelCursorMaxEnvelope {
		return "", false
	}
	return base64.RawURLEncoding.EncodeToString(envelope), true
}

func (s *ControlPlaneServer) controlPlanePanelRateLimit(
	w http.ResponseWriter,
	r *http.Request,
	scope, key string,
	limit int,
	window, block time.Duration,
) bool {
	decision, err := s.business.ConsumeRateLimit(r.Context(), RateLimitCommand{
		Scope: scope, Key: key, Limit: limit, Window: window, Block: block,
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return false
	}
	if decision.Allowed {
		return true
	}
	retry := decision.RetryAfterSeconds
	if retry < 1 {
		retry = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	writeControlPlaneJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	return false
}

func (s *ControlPlaneServer) registerControlPlanePanel(mux *http.ServeMux) {
	prefix := s.cfg.PanelPath
	if prefix == "" || s.cfg.PanelPasswordHash == "" {
		return
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	mux.HandleFunc(prefix, s.handleControlPlanePanelApp)
	mux.HandleFunc(prefix+"api/login", s.handleControlPlanePanelLogin)
	mux.HandleFunc(prefix+"api/logout", s.handleControlPlanePanelLogout)
	mux.HandleFunc(prefix+"api/me", s.handleControlPlanePanelMe)
	mux.HandleFunc(prefix+"api/password", s.handleControlPlanePanelPassword)
	mux.HandleFunc(prefix+"api/customers", s.handleControlPlanePanelCustomers)
	mux.HandleFunc(prefix+"api/customer", s.handleControlPlanePanelCustomer)
	mux.HandleFunc(prefix+"api/stats", s.handleControlPlanePanelStats)
	mux.HandleFunc(prefix+"api/orders", s.handleControlPlanePanelOrders)
	mux.HandleFunc(prefix+"api/order/confirm", s.handleControlPlanePanelOrderConfirm)
	mux.HandleFunc(prefix+"api/order/cancel", s.handleControlPlanePanelOrderCancel)
	mux.HandleFunc(prefix+"api/cluster-status", s.handleControlPlanePanelClusterStatus)
	mux.HandleFunc(prefix+"api/audit", s.handleControlPlanePanelAudit)
	mux.HandleFunc(prefix+"api/action", s.handleControlPlanePanelAction)
	mux.HandleFunc(prefix+"api/olcrtc", s.handleControlPlanePanelOLCRTC)
	mux.HandleFunc(prefix+"api/olcrtc/room", s.handleControlPlanePanelOLCRTCRoom)
	mux.HandleFunc(prefix+"api/olcrtc/login", s.handleControlPlanePanelOLCRTCGrant)
	mux.HandleFunc(prefix+"api/olcrtc/wbtoken", s.handleControlPlanePanelWBToken)
	mux.HandleFunc(prefix+"api/olcrtc/wbroom", s.handleControlPlanePanelWBRoom)
	mux.HandleFunc(prefix+"api/vkturn", s.handleControlPlanePanelVKTurn)
	mux.HandleFunc(prefix+"api/vkturn/enabled", s.handleControlPlanePanelVKTurnEnabled)
}

func (s *ControlPlaneServer) handleControlPlanePanelApp(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(panelHTML))
}

func (s *ControlPlaneServer) handleControlPlanePanelLogin(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	if !s.controlPlanePanelRateLimit(w, r, "panel.login.ip", panelClientIP(r), controlPlanePanelLoginIPLimit, 10*time.Minute, 15*time.Minute) {
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	view, err := s.business.CreateSession(r.Context(), CreateSessionCommand{Password: request.Password})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: controlPlanePanelCookie, Value: view.Token, Path: s.cfg.PanelPath,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: controlPlanePanelCSRFCookie, Value: view.CSRF, Path: s.cfg.PanelPath,
		Secure: true, SameSite: http.SameSiteStrictMode,
	})
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true, "csrf": view.CSRF})
}

func (s *ControlPlaneServer) controlPlanePanelGuard(w http.ResponseWriter, r *http.Request, write bool) (PrincipalView, bool) {
	permission := "read"
	if write {
		permission = "write"
	}
	return s.controlPlanePanelGuardPermission(w, r, permission, write)
}

type controlPlanePanelCredentials struct {
	session    string
	csrf       string
	actorScope string
	actorLimit int
}

func (s *ControlPlaneServer) controlPlanePanelPreflight(w http.ResponseWriter, r *http.Request, write bool) (controlPlanePanelCredentials, bool) {
	ipScope := "panel.read.ip"
	actorScope := "panel.read.actor"
	ipLimit := controlPlanePanelReadIPLimit
	actorLimit := controlPlanePanelReadActorLimit
	if write {
		ipScope = "panel.write.ip"
		actorScope = "panel.write.actor"
		ipLimit = controlPlanePanelWriteIPLimit
		actorLimit = controlPlanePanelWriteActorLimit
	}
	if !s.controlPlanePanelRateLimit(w, r, ipScope, panelClientIP(r), ipLimit, time.Minute, 5*time.Minute) {
		return controlPlanePanelCredentials{}, false
	}
	cookie, err := r.Cookie(controlPlanePanelCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		writeControlPlaneJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return controlPlanePanelCredentials{}, false
	}
	csrf := strings.TrimSpace(r.Header.Get("X-CSRF"))
	if csrf == "" && !write {
		if csrfCookie, cookieErr := r.Cookie(controlPlanePanelCSRFCookie); cookieErr == nil {
			csrf = strings.TrimSpace(csrfCookie.Value)
		}
	}
	if csrf == "" && write {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "bad csrf"})
		return controlPlanePanelCredentials{}, false
	}
	if write && strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		writeControlPlaneJSON(w, http.StatusPreconditionRequired, map[string]string{"error": "Idempotency-Key required"})
		return controlPlanePanelCredentials{}, false
	}
	return controlPlanePanelCredentials{
		session: cookie.Value, csrf: csrf, actorScope: actorScope, actorLimit: actorLimit,
	}, true
}

func (s *ControlPlaneServer) controlPlanePanelAuthorize(w http.ResponseWriter, r *http.Request, credentials controlPlanePanelCredentials, permission string) (PrincipalView, bool) {
	principal, err := s.business.Authorize(r.Context(), AuthorizeCommand{
		Session: credentials.session, CSRF: credentials.csrf, Permission: permission,
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return PrincipalView{}, false
	}
	if !s.controlPlanePanelRateLimit(w, r, credentials.actorScope, principal.ID, credentials.actorLimit, time.Minute, 5*time.Minute) {
		return PrincipalView{}, false
	}
	return principal, true
}

func (s *ControlPlaneServer) controlPlanePanelGuardPermission(w http.ResponseWriter, r *http.Request, permission string, write bool) (PrincipalView, bool) {
	credentials, ok := s.controlPlanePanelPreflight(w, r, write)
	if !ok {
		return PrincipalView{}, false
	}
	return s.controlPlanePanelAuthorize(w, r, credentials, permission)
}

func (s *ControlPlaneServer) handleControlPlanePanelLogout(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	principal, ok := s.controlPlanePanelGuardPermission(w, r, "read", true)
	if !ok {
		return
	}
	if err := s.business.RevokeSessions(r.Context(), RevokeSessionsCommand{PrincipalID: principal.ID, Actor: principal.ID}); err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: controlPlanePanelCookie, Value: "", Path: s.cfg.PanelPath, MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: controlPlanePanelCSRFCookie, Value: "", Path: s.cfg.PanelPath, MaxAge: -1, Secure: true, SameSite: http.SameSiteStrictMode})
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *ControlPlaneServer) handleControlPlanePanelMe(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	principal, ok := s.controlPlanePanelGuard(w, r, false)
	if !ok {
		return
	}
	csrfCookie, _ := r.Cookie(controlPlanePanelCSRFCookie)
	csrf := ""
	if csrfCookie != nil {
		csrf = strings.TrimSpace(csrfCookie.Value)
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{
		"logged_in": true, "csrf": csrf, "id": principal.ID, "permissions": principal.Permissions,
	})
}

func (s *ControlPlaneServer) handleControlPlanePanelPassword(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	principal, ok := s.controlPlanePanelGuard(w, r, true)
	if !ok {
		return
	}
	var request struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	err := s.business.ChangePrincipalPassword(r.Context(), ChangePasswordCommand{
		PrincipalID: principal.ID, Current: request.Current, New: request.New,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *ControlPlaneServer) handleControlPlanePanelCustomers(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, false); !ok {
		return
	}
	limit, cursor, valid := s.controlPlanePanelPage(r, "customers")
	if !valid {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pagination"})
		return
	}
	customers, err := s.business.ListCustomers(r.Context(), CustomerFilter{
		Limit: limit + 1, AfterLogin: cursor.Key, AfterCustomerID: cursor.ID,
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	nextCursor := ""
	if len(customers) > limit {
		customers = customers[:limit]
		last := customers[len(customers)-1]
		if last.CustomerID == "" {
			writeControlPlaneBusinessError(w, businessError(controlplane.ErrUnavailable))
			return
		}
		encodedCursor, encoded := s.encodeControlPlanePanelCursor(controlPlanePanelCursor{Kind: "customers", Key: last.Login, ID: last.CustomerID})
		if !encoded {
			writeControlPlaneBusinessError(w, businessError(controlplane.ErrUnavailable))
			return
		}
		nextCursor = encodedCursor
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"customers": toPanelCustomers(customers), "next_cursor": nextCursor})
}

func (s *ControlPlaneServer) handleControlPlanePanelCustomer(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, false); !ok {
		return
	}
	login := r.URL.Query().Get("login")
	customer, err := s.business.CustomerByLogin(r.Context(), login)
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	usage, err := s.business.CustomerUsage(r.Context(), login)
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{
		"customer": toPanelCustomer(customer), "device_ids": usage.DeviceIDs,
		"device_limit": usage.DeviceLimit, "traffic_bytes": usage.Bytes,
	})
}

func (s *ControlPlaneServer) handleControlPlanePanelStats(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, false); !ok {
		return
	}
	stats, err := s.business.CustomerStats(r.Context())
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, stats)
}

func (s *ControlPlaneServer) handleControlPlanePanelOrders(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := s.controlPlanePanelGuardPermission(w, r, "payment.decide", false); !ok {
		return
	}
	rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	limit, cursor, valid := s.controlPlanePanelPage(r, "orders")
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if !valid || (rawCursor != "" && cursor.Filter != status) {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pagination"})
		return
	}
	orders, err := s.business.ListOrders(r.Context(), OrderFilter{
		Status: status, Limit: limit + 1, AfterCreatedAtUnix: cursor.Unix, AfterOrderID: cursor.ID,
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	nextCursor := ""
	if len(orders) > limit {
		orders = orders[:limit]
		last := orders[len(orders)-1]
		if last.CreatedAtUnix < 1 || last.OrderID == "" {
			writeControlPlaneBusinessError(w, businessError(controlplane.ErrUnavailable))
			return
		}
		encodedCursor, encoded := s.encodeControlPlanePanelCursor(controlPlanePanelCursor{
			Kind: "orders", ID: last.OrderID, Unix: last.CreatedAtUnix, Filter: status,
		})
		if !encoded {
			writeControlPlaneBusinessError(w, businessError(controlplane.ErrUnavailable))
			return
		}
		nextCursor = encodedCursor
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"orders": toPanelOrders(orders), "next_cursor": nextCursor})
}

func (s *ControlPlaneServer) handleControlPlanePanelOrderConfirm(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	principal, ok := s.controlPlanePanelGuardPermission(w, r, "payment.decide", true)
	if !ok {
		return
	}
	var request struct {
		OrderID string `json:"order_id"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	result, err := s.business.ConfirmPayment(r.Context(), ConfirmPaymentCommand{
		OrderID: request.OrderID, Actor: principal.ID, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, panelConfirmPaymentResult{
		Order: toPanelOrder(result.Order), Customer: toPanelCustomer(result.Customer), Operation: result.Operation,
	})
}

func (s *ControlPlaneServer) handleControlPlanePanelOrderCancel(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	principal, ok := s.controlPlanePanelGuardPermission(w, r, "payment.decide", true)
	if !ok {
		return
	}
	var request struct {
		OrderID string `json:"order_id"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	order, err := s.business.CancelOrder(r.Context(), CancelOrderCommand{
		OrderID: request.OrderID, Actor: principal.ID, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, toPanelOrder(order))
}

func (s *ControlPlaneServer) handleControlPlanePanelClusterStatus(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := s.controlPlanePanelGuardPermission(w, r, "settings.critical", false); !ok {
		return
	}
	status, err := s.business.ClusterStatus(r.Context())
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, status)
}

func (s *ControlPlaneServer) handleControlPlanePanelAudit(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := s.controlPlanePanelGuardPermission(w, r, "settings.critical", false); !ok {
		return
	}
	limit, cursor, valid := s.controlPlanePanelPage(r, "audit")
	if !valid || cursor.Filter != "" {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pagination"})
		return
	}
	events, err := s.business.RecentAudit(r.Context(), AuditFilter{
		Limit: limit + 1, AfterCreatedAtUnix: cursor.Unix, AfterID: cursor.ID,
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	nextCursor := ""
	if len(events) > limit {
		events = events[:limit]
		last := events[len(events)-1]
		if last.CreatedAt.IsZero() || last.ID == "" {
			writeControlPlaneBusinessError(w, businessError(controlplane.ErrUnavailable))
			return
		}
		encodedCursor, encoded := s.encodeControlPlanePanelCursor(controlPlanePanelCursor{Kind: "audit", ID: last.ID, Unix: last.CreatedAt.Unix()})
		if !encoded {
			writeControlPlaneBusinessError(w, businessError(controlplane.ErrUnavailable))
			return
		}
		nextCursor = encodedCursor
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"events": events, "next_cursor": nextCursor})
}

func (s *ControlPlaneServer) handleControlPlanePanelAction(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	credentials, ok := s.controlPlanePanelPreflight(w, r, true)
	if !ok {
		return
	}
	var request struct {
		Action  string `json:"action"`
		Login   string `json:"login"`
		Days    int    `json:"days"`
		Expires string `json:"expires"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	permission := "settings.critical"
	switch request.Action {
	case "provision", "extend", "renew", "set_expiry", "reset_devices":
		permission = "customer.provision"
	}
	if _, ok := s.controlPlanePanelAuthorize(w, r, credentials, permission); !ok {
		return
	}
	idempotency := r.Header.Get("Idempotency-Key")
	var (
		view CustomerView
		err  error
	)
	switch request.Action {
	case "provision":
		view, err = s.business.ProvisionCustomer(r.Context(), ProvisionCustomerCommand{Login: request.Login, Days: request.Days, IdempotencyKey: idempotency})
	case "extend":
		view, err = s.business.ExtendCustomer(r.Context(), ExtendCustomerCommand{Login: request.Login, Days: request.Days, IdempotencyKey: idempotency})
	case "renew":
		view, err = s.business.RenewCustomer(r.Context(), RenewCustomerCommand{Login: request.Login, Days: request.Days, IdempotencyKey: idempotency})
	case "set_expiry":
		expires, parseErr := timeParseRFC3339(request.Expires)
		if parseErr != nil {
			writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "expires must be RFC3339"})
			return
		}
		view, err = s.business.SetCustomerExpiry(r.Context(), SetExpiryCommand{Login: request.Login, Expires: expires, IdempotencyKey: idempotency})
	case "reset_devices":
		err = s.business.ResetDevices(r.Context(), ResetDevicesCommand{Login: request.Login, IdempotencyKey: idempotency})
		if err == nil {
			view, err = s.business.CustomerByLogin(r.Context(), request.Login)
		}
	case "disable":
		view, err = s.business.DisableCustomer(r.Context(), CustomerStateCommand{Login: request.Login, IdempotencyKey: idempotency})
	case "enable":
		view, err = s.business.EnableCustomer(r.Context(), CustomerStateCommand{Login: request.Login, IdempotencyKey: idempotency})
	case "delete":
		err = s.business.DeleteCustomer(r.Context(), DeleteCustomerCommand{Login: request.Login, IdempotencyKey: idempotency})
		if err == nil {
			writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	case "delete_expired":
		operation, sweepErr := s.business.RunExpirySweep(r.Context(), ExpirySweepCommand{IdempotencyKey: idempotency})
		if sweepErr != nil {
			writeControlPlaneBusinessError(w, sweepErr)
			return
		}
		writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true, "operation": operation, "deleted": operation.Count})
		return
	default:
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
		return
	}
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true, "customer": toPanelCustomer(view)})
}

func (s *ControlPlaneServer) handleControlPlanePanelOLCRTC(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, false); !ok {
		return
	}
	state, err := s.business.OLCRTCState(r.Context())
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	secret, err := s.business.WBTokenStatus(r.Context())
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"olcrtc": state, "wb_token_set": secret.Configured})
}

func (s *ControlPlaneServer) handleControlPlanePanelOLCRTCRoom(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, true); !ok {
		return
	}
	var request struct {
		Login           string `json:"login"`
		Room            string `json:"room"`
		Provider        string `json:"provider"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
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

func (s *ControlPlaneServer) handleControlPlanePanelOLCRTCGrant(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, true); !ok {
		return
	}
	var request struct {
		Login           string `json:"login"`
		Action          string `json:"action"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	if request.Action != "add" && request.Action != "remove" {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be add or remove"})
		return
	}
	view, err := s.business.SetOLCRTCGrant(r.Context(), SetOLCRTCGrantCommand{
		Login: request.Login, Enabled: request.Action == "add", ExpectedVersion: request.ExpectedVersion,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlanePanelWBToken(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, true); !ok {
		return
	}
	var request struct {
		Token           string `json:"token"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	err := s.business.SetWBToken(r.Context(), SetSecretCommand{
		Secret: request.Token, ExpectedVersion: request.ExpectedVersion,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *ControlPlaneServer) handleControlPlanePanelWBRoom(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, true); !ok {
		return
	}
	var request struct {
		Login             string `json:"login"`
		ActionKey         string `json:"action_key"`
		ReplacesActionKey string `json:"replaces_action_key"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	if request.ActionKey == "" {
		request.ActionKey = r.Header.Get("Idempotency-Key")
	}
	view, err := s.business.RequestWBRoom(r.Context(), RequestWBRoomCommand{
		Login: request.Login, ActionKey: request.ActionKey, ReplacesActionKey: request.ReplacesActionKey,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlanePanelVKTurn(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if _, ok := s.controlPlanePanelGuard(w, r, false); !ok {
			return
		}
		view, err := s.business.VKTurnState(r.Context())
		if err != nil {
			writeControlPlaneBusinessError(w, err)
			return
		}
		writeControlPlaneJSON(w, http.StatusOK, view)
		return
	}
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, true); !ok {
		return
	}
	var value json.RawMessage
	if !decodeControlPlaneBody(w, r, &value) {
		return
	}
	view, err := s.business.UpdateVKTurn(r.Context(), UpdateVKTurnCommand{
		Value: value, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlanePanelVKTurnEnabled(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, true); !ok {
		return
	}
	var request struct {
		Enabled         bool  `json:"enabled"`
		ExpectedVersion int64 `json:"expected_version"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	view, err := s.business.SetVKTurnEnabled(r.Context(), SetVKTurnEnabledCommand{
		Enabled: request.Enabled, ExpectedVersion: request.ExpectedVersion,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func timeParseRFC3339(value string) (time.Time, error) {
	return time.Parse(time.RFC3339, value)
}
