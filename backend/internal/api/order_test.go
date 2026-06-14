package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

func TestOrderPurchaseFlow(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.json"))
	ost, _ := order.Open(filepath.Join(t.TempDir(), "o.json"))
	srv := httptest.NewServer(New(st, &fakeProv{st: st}, ost, Config{
		AdminToken: "sek", SubBaseURL: "https://wapmixx.ru:8910", SBPPhone: "+79991234567",
	}).Handler())
	defer srv.Close()

	// 1. create order
	resp, err := http.Post(srv.URL+"/order", "application/json", strings.NewReader(`{"tariff":"2m"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created struct {
		OrderID  string `json:"order_id"`
		Code     string `json:"code"`
		SBPPhone string `json:"sbp_phone"`
		Rub      int    `json:"rub"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	if created.OrderID == "" || created.Rub != 600 || created.SBPPhone != "+79991234567" || created.Code == "" {
		t.Fatalf("create order bad: %+v", created)
	}

	// 2. poll: pending, no sub_url yet
	var got struct {
		Status string `json:"status"`
		SubURL string `json:"sub_url"`
	}
	r2, _ := http.Get(srv.URL + "/order/" + created.OrderID)
	_ = json.NewDecoder(r2.Body).Decode(&got)
	_ = r2.Body.Close()
	if got.Status != "pending" || got.SubURL != "" {
		t.Fatalf("want pending+no url, got %+v", got)
	}

	// 3. admin confirms payment -> provisions + paid
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/order/confirm",
		strings.NewReader(`{"order_id":"`+created.OrderID+`"}`))
	req.Header.Set("Authorization", "Bearer sek")
	rc, _ := http.DefaultClient.Do(req)
	if rc.StatusCode != http.StatusOK {
		t.Fatalf("confirm status = %d, want 200", rc.StatusCode)
	}
	_ = rc.Body.Close()

	// 4. poll: paid with sub_url
	r3, _ := http.Get(srv.URL + "/order/" + created.OrderID)
	_ = json.NewDecoder(r3.Body).Decode(&got)
	_ = r3.Body.Close()
	if got.Status != "paid" || !strings.HasPrefix(got.SubURL, "https://wapmixx.ru:8910/sub/") {
		t.Fatalf("want paid+sub_url, got %+v", got)
	}
}

func TestOrderUnknownTariff(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.json"))
	ost, _ := order.Open(filepath.Join(t.TempDir(), "o.json"))
	srv := httptest.NewServer(New(st, &fakeProv{st: st}, ost, Config{}).Handler())
	defer srv.Close()
	resp, _ := http.Post(srv.URL+"/order", "application/json", strings.NewReader(`{"tariff":"99y"}`))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown tariff status = %d, want 400", resp.StatusCode)
	}
}
