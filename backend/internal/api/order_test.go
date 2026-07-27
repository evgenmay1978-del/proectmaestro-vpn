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
	srv := httptest.NewServer(New(st, &fakeProv{st: st}, ost, nil, Config{
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
	srv := httptest.NewServer(New(st, fp, ost, nil, Config{
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
	srv := httptest.NewServer(New(st, &fakeProv{st: st}, ost, nil, Config{}).Handler())
	defer srv.Close()
	resp, _ := http.Post(srv.URL+"/order", "application/json", strings.NewReader(`{"tariff":"99y"}`))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown tariff status = %d, want 400", resp.StatusCode)
	}
}

// flakyProv воспроизводит реальный сбой: дни ложатся в хранилище, а раскатка по узлам падает.
// Именно этот случай раньше приводил к двойному начислению — заказ оставался pending, и повтор
// подтверждения складывал срок ещё раз.
type flakyProv struct {
	*fakeProv
	failFanOut bool
	extends    int
	setExpiry  int
}

func (f *flakyProv) Extend(login string, dur time.Duration) (*store.Customer, error) {
	f.extends++
	c, err := f.st.Extend(login, dur) // дни УЖЕ начислены
	if err != nil {
		return nil, err
	}
	if f.failFanOut {
		return c, errAssertFanOut // клиент возвращается вместе с ошибкой, как в бою
	}
	return c, nil
}

func (f *flakyProv) SetExpiry(login string, t time.Time) (*store.Customer, error) {
	f.setExpiry++
	return f.st.SetExpiry(login, t)
}

var errAssertFanOut = errAssert("fan-out failed")

type errAssert string

func (e errAssert) Error() string { return string(e) }

// Повторное подтверждение после сорвавшейся раскатки НЕ должно начислять дни второй раз.
func TestOrderConfirmRetryDoesNotDoubleCredit(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.json"))
	ost, _ := order.Open(filepath.Join(t.TempDir(), "o.json"))
	prov := &flakyProv{fakeProv: &fakeProv{st: st}, failFanOut: true}
	srv := httptest.NewServer(New(st, prov, ost, nil, Config{
		AdminToken: "sek", SubBaseURL: "https://x", SBPPhone: "+7",
	}).Handler())
	defer srv.Close()

	// Уже существующий клиент — ветка продления, где Extend СКЛАДЫВАЕТ дни.
	base := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	if err := st.Put(&store.Customer{Login: "renewme", SubToken: "tok", Expires: base}); err != nil {
		t.Fatalf("put: %v", err)
	}
	o, err := ost.CreateFor(order.DefaultTariffs[0], "renewme")
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	days := o.Days

	confirm := func() int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/order/confirm",
			strings.NewReader(`{"order_id":"`+o.ID+`"}`))
		req.Header.Set("Authorization", "Bearer sek")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("confirm: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// Первая попытка: дни начислены, раскатка упала → 502, заказ остаётся неоплаченным.
	if code := confirm(); code != http.StatusBadGateway {
		t.Fatalf("первая попытка: ожидался 502, получен %d", code)
	}
	afterFirst, err := st.ByLogin("renewme")
	if err != nil {
		t.Fatalf("by login: %v", err)
	}
	wantFirst := base.Add(time.Duration(days) * 24 * time.Hour)
	if afterFirst.Expires.Sub(wantFirst).Abs() > time.Minute {
		t.Fatalf("после первой попытки дата %v, ожидалась ~%v", afterFirst.Expires, wantFirst)
	}
	if got, _ := ost.ByID(o.ID); !got.Credited {
		t.Fatal("признак «начислено» не сохранён — повтор начислит второй раз")
	}

	// Раскатка починилась; владелец повторяет подтверждение.
	prov.failFanOut = false
	if code := confirm(); code != http.StatusOK {
		t.Fatalf("вторая попытка: ожидался 200, получен %d", code)
	}
	afterSecond, err := st.ByLogin("renewme")
	if err != nil {
		t.Fatalf("by login 2: %v", err)
	}
	// Ключевая проверка: дата НЕ уехала ещё на один период.
	if afterSecond.Expires.Sub(wantFirst).Abs() > time.Minute {
		t.Fatalf("ДВОЙНОЕ НАЧИСЛЕНИЕ: дата %v, ожидалась ~%v (%d дней за один платёж)",
			afterSecond.Expires, wantFirst, days)
	}
	if prov.extends != 1 {
		t.Fatalf("Extend вызван %d раз, ожидался ровно 1", prov.extends)
	}
	if prov.setExpiry != 1 {
		t.Fatalf("SetExpiry (добивание раскатки) вызван %d раз, ожидался 1", prov.setExpiry)
	}
}
