package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if created.OrderID == "" || created.Rub != 800 || created.SBPPhone != "+79991234567" || created.Code == "" {
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

// TestOrderRenewsSameAccount: an order that carries an existing customer's sub_token
// must RENEW that same account (Extend, stacking days, same sub URL) on confirm —
// not mint a brand-new account. This is the in-app renewal the owner asked for.
func TestOrderRenewsSameAccount(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.json"))
	ost, _ := order.Open(filepath.Join(t.TempDir(), "o.json"))
	fp := &fakeProv{st: st}
	// existing customer with ~30 days left (sub_token = "tok-alice" per fakeProv)
	if _, err := fp.Provision("alice", 30*24*time.Hour); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := httptest.NewServer(New(st, fp, ost, Config{
		AdminToken: "sek", SubBaseURL: "https://wapmixx.ru:8910", SBPPhone: "+7",
	}).Handler())
	defer srv.Close()

	// create an order carrying alice's sub_token → it must target alice's account
	resp, err := http.Post(srv.URL+"/order", "application/json",
		strings.NewReader(`{"tariff":"1m","sub_token":"tok-alice"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created struct {
		OrderID string `json:"order_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	if created.OrderID == "" {
		t.Fatal("no order id")
	}

	// confirm → must EXTEND alice (same sub URL), not create a new account
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/order/confirm",
		strings.NewReader(`{"order_id":"`+created.OrderID+`"}`))
	req.Header.Set("Authorization", "Bearer sek")
	rc, _ := http.DefaultClient.Do(req)
	var confirmed struct {
		Login  string `json:"login"`
		SubURL string `json:"sub_url"`
	}
	_ = json.NewDecoder(rc.Body).Decode(&confirmed)
	_ = rc.Body.Close()
	if confirmed.Login != "alice" {
		t.Fatalf("renewal targeted login %q, want alice (a new account was minted)", confirmed.Login)
	}
	if !strings.HasSuffix(confirmed.SubURL, "/sub/tok-alice") {
		t.Fatalf("renewal sub_url = %q, want the SAME account /sub/tok-alice", confirmed.SubURL)
	}
	// expiry must be STACKED (~60d) — proves Extend ran (Provision would reset to ~30d)
	c, _ := st.ByLogin("alice")
	if got := time.Until(c.Expires); got < 45*24*time.Hour {
		t.Fatalf("expiry not stacked: %v left, want ~60d (Extend, not Provision)", got)
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
