package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const controlPlanePanelCookie = "mp_session"

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
	var request struct {
		Password string `json:"password"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	view, err := s.business.CreateSession(r.Context(), CreateSessionCommand{Password: request.Password})
	if err != nil {
		writeControlPlaneJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: controlPlanePanelCookie, Value: view.Token, Path: s.cfg.PanelPath,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true, "csrf": view.CSRF})
}

func (s *ControlPlaneServer) controlPlanePanelGuard(w http.ResponseWriter, r *http.Request, write bool) (PrincipalView, bool) {
	cookie, err := r.Cookie(controlPlanePanelCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		writeControlPlaneJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return PrincipalView{}, false
	}
	csrf := r.Header.Get("X-CSRF")
	if write && strings.TrimSpace(csrf) == "" {
		writeControlPlaneJSON(w, http.StatusForbidden, map[string]string{"error": "bad csrf"})
		return PrincipalView{}, false
	}
	if write && strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		writeControlPlaneJSON(w, http.StatusPreconditionRequired, map[string]string{"error": "Idempotency-Key required"})
		return PrincipalView{}, false
	}
	permission := "read"
	if write {
		permission = "write"
	}
	principal, err := s.business.Authorize(r.Context(), AuthorizeCommand{
		Session: cookie.Value, CSRF: csrf, Permission: permission,
	})
	if err != nil {
		writeControlPlaneJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return PrincipalView{}, false
	}
	return principal, true
}

func (s *ControlPlaneServer) handleControlPlanePanelLogout(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	principal, ok := s.controlPlanePanelGuard(w, r, true)
	if !ok {
		return
	}
	if err := s.business.RevokeSessions(r.Context(), RevokeSessionsCommand{PrincipalID: principal.ID, Actor: principal.ID}); err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: controlPlanePanelCookie, Value: "", Path: s.cfg.PanelPath, MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
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
	writeControlPlaneJSON(w, http.StatusOK, principal)
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
	customers, err := s.business.ListCustomers(r.Context(), CustomerFilter{})
	if err != nil {
		writeControlPlaneBusinessError(w, err)
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"customers": customers})
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
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"customer": customer, "usage": usage})
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

func (s *ControlPlaneServer) handleControlPlanePanelAction(w http.ResponseWriter, r *http.Request) {
	if !requireControlPlaneMethod(w, r, http.MethodPost) {
		return
	}
	if _, ok := s.controlPlanePanelGuard(w, r, true); !ok {
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
	case "disable":
		view, err = s.business.DisableCustomer(r.Context(), CustomerStateCommand{Login: request.Login, IdempotencyKey: idempotency})
	case "enable":
		view, err = s.business.EnableCustomer(r.Context(), CustomerStateCommand{Login: request.Login, IdempotencyKey: idempotency})
	case "delete":
		err = s.business.DeleteCustomer(r.Context(), DeleteCustomerCommand{Login: request.Login, IdempotencyKey: idempotency})
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
	writeControlPlaneJSON(w, http.StatusOK, map[string]any{"ok": true, "customer": view})
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
		Login     string `json:"login"`
		ActionKey string `json:"action_key"`
	}
	if !decodeControlPlaneBody(w, r, &request) {
		return
	}
	if request.ActionKey == "" {
		request.ActionKey = r.Header.Get("Idempotency-Key")
	}
	view, err := s.business.RequestWBRoom(r.Context(), RequestWBRoomCommand{
		Login: request.Login, ActionKey: request.ActionKey, IdempotencyKey: r.Header.Get("Idempotency-Key"),
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
