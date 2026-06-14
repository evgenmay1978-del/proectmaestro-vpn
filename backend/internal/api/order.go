package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
)

// handleTariffs lists the purchasable plans + the СБП phone (public).
func (s *Server) handleTariffs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tariffs":   order.DefaultTariffs,
		"sbp_phone": s.cfg.SBPPhone,
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
		Tariff string `json:"tariff"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	t, ok := order.TariffByKey(req.Tariff)
	if !ok {
		http.Error(w, "unknown tariff", http.StatusBadRequest)
		return
	}
	o, err := s.orders.Create(t)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"order_id": o.ID, "code": o.Code, "rub": o.Rub, "days": o.Days,
		"tariff": t.Name, "sbp_phone": s.cfg.SBPPhone, "status": o.Status,
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

// handleOrderConfirm (admin): the owner confirms a received СБП payment → provision
// the customer and flip the order to paid, so the app's poll returns the sub_url.
func (s *Server) handleOrderConfirm(w http.ResponseWriter, r *http.Request) {
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
	if o.Status == "paid" {
		writeJSON(w, http.StatusOK, map[string]any{"order_id": o.ID, "status": "paid", "login": o.Login})
		return
	}
	cust, err := s.prov.Provision(o.Login, time.Duration(o.Days)*24*time.Hour)
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
