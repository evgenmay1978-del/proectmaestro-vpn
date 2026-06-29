package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

// handleTariffs lists the purchasable plans + the СБП phone (public).
func (s *Server) handleTariffs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tariffs":   order.DefaultTariffs,
		"sbp_phone": s.cfg.SBPPhone,
		"pay_url":   s.cfg.PayURL,
	})
}

// handleOrderCreate (public): POST {tariff} → a pending order with СБП details the
// TV shows. The customer pays from their phone with Code as the transfer comment.
func (s *Server) handleOrderCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Tariff   string `json:"tariff"`
		SubToken string `json:"sub_token"` // present → RENEW this existing account
		Login    string `json:"login"`     // alternative identity if the app knows it
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	t, ok := order.TariffByKey(req.Tariff)
	if !ok {
		http.Error(w, "unknown tariff", http.StatusBadRequest)
		return
	}
	// Decide which account this order renews. The app sends the sub_token from its
	// active profile; we map it to the customer's login. Unknown or empty → a brand-new
	// account (first-time purchase is unchanged), so this is fully backward-compatible.
	login := ""
	if req.SubToken != "" {
		if c, e := s.st.ByToken(req.SubToken); e == nil && c != nil {
			login = c.Login
		}
	}
	if login == "" && req.Login != "" {
		if c, e := s.st.ByLogin(req.Login); e == nil && c != nil {
			login = c.Login
		}
	}
	o, err := s.orders.CreateFor(t, login)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"order_id": o.ID, "code": o.Code, "rub": o.Rub, "days": o.Days,
		"tariff": t.Name, "sbp_phone": s.cfg.SBPPhone, "pay_url": s.cfg.PayURL, "status": o.Status,
	})
}

// handleOrderGet (public): GET /order/<id> → status, and sub_url once paid. The app
// polls this after showing the payment details.
func (s *Server) handleOrderGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/order/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	o, err := s.orders.ByID(id)
	if errors.Is(err, order.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := map[string]any{"order_id": o.ID, "status": o.Status, "rub": o.Rub, "code": o.Code}
	if o.Status == "paid" && o.SubToken != "" {
		resp["sub_url"] = s.cfg.SubBaseURL + "/sub/" + o.SubToken
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleOrderPaidClaim (public): the customer pressed «Я оплатил» — notify the
// owner via the bot with a one-tap confirm button. The order stays pending until
// the owner confirms; the app keeps polling /order/<id>.
func (s *Server) handleOrderPaidClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		OrderID string `json:"order_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	o, err := s.orders.ByID(req.OrderID)
	if errors.Is(err, order.ErrNotFound) {
		http.Error(w, "no such order", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	text := fmt.Sprintf("💳 Оплата подписки\nТариф: %s · %d ₽\nКод платежа: %s\nЗаказ: %s\n\nКлиент нажал «Я оплатил». Проверьте поступление по СБП и подтвердите.",
		o.Tariff, o.Rub, o.Code, o.ID)
	_ = s.tg.NotifyOrder(s.cfg.TGAdminID, text, o.ID)
	writeJSON(w, http.StatusOK, map[string]any{"status": "awaiting_confirm"})
}

// confirmMu serializes order confirmations so a double-tapped Telegram «Подтвердить»
// button (or a retry) can't let two requests both pass the pending→paid gate and
// double-provision — for a RENEWAL store.Extend STACKS days (≈60 instead of 30). The
// second caller waits, re-reads the order as already "paid", and early-returns. Confirms
// are rare + admin-only, so one mutex for the whole confirm path is fine.
var confirmMu sync.Mutex

// handleOrderConfirm (admin): the owner confirms a received СБП payment → provision
// the customer and flip the order to paid, so the app's poll returns the sub_url.
func (s *Server) handleOrderConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrderID string `json:"order_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	confirmMu.Lock()
	defer confirmMu.Unlock()
	o, err := s.orders.ByID(req.OrderID)
	if errors.Is(err, order.ErrNotFound) {
		http.Error(w, "no such order", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if o.Status == "paid" {
		writeJSON(w, http.StatusOK, map[string]any{"order_id": o.ID, "status": "paid", "login": o.Login})
		return
	}
	dur := time.Duration(o.Days) * 24 * time.Hour
	// RENEW the same account if its login already exists (Extend stacks the days from
	// max(now, current expiry) and re-writes every panel — 3x-ui date + Hy2/Naive
	// membership); otherwise provision a fresh account. This is what makes an in-app
	// renewal keep the customer's existing subscription instead of orphaning it.
	var cust *store.Customer
	if existing, e := s.st.ByLogin(o.Login); e == nil && existing != nil {
		cust, err = s.prov.Extend(o.Login, dur)
	} else {
		cust, err = s.prov.Provision(o.Login, dur)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if _, err := s.orders.MarkPaid(o.ID, cust.SubToken); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"order_id": o.ID, "status": "paid", "login": o.Login,
		"sub_url": s.cfg.SubBaseURL + "/sub/" + cust.SubToken,
	})
}

// handleOrderCancel (admin): the owner saw no payment → drop the pending order so
// abandoned "Купить" taps don't pile up. A paid order is refused.
func (s *Server) handleOrderCancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrderID string `json:"order_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.orders.Cancel(req.OrderID); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order_id": req.OrderID, "status": "canceled"})
}
