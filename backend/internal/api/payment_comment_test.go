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

// SBP-комментарий к переводу — ЛОГИН клиента, а не случайный код: владелец сверяет
// поступление по комментарию, а клиент часто не нажимает «Я оплатил» (14.11.2026, MANV8Q).
// Code остаётся уникальным внутренним идентификатором (orders.payment_code в контроль-плейне UNIQUE).
func TestOrderPaymentCommentIsLogin(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.json"))
	ost, _ := order.Open(filepath.Join(t.TempDir(), "o.json"))
	if err := st.Put(&store.Customer{Login: "alice", SubToken: "tok-alice", Expires: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := httptest.NewServer(New(st, &fakeProv{st: st}, ost, nil, Config{AdminToken: "sek", SubBaseURL: "https://x", SBPPhone: "+7"}).Handler())
	defer srv.Close()

	create := func(body string) (map[string]any, string) {
		resp, err := http.Post(srv.URL+"/order", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		id, _ := out["order_id"].(string)
		return out, id
	}

	// Продление: логин известен из sub_token — он и есть комментарий к переводу.
	renew, renewID := create(`{"tariff":"1m","sub_token":"tok-alice"}`)
	if renew["code"] != "alice" {
		t.Fatalf("продление: комментарий %v, ожидался логин alice", renew["code"])
	}
	// Первая покупка: панель сама создаёт логин tv-… — комментарий равен ему.
	first, firstID := create(`{"tariff":"1m"}`)
	code, _ := first["code"].(string)
	if !strings.HasPrefix(code, "tv-") {
		t.Fatalf("первая покупка: комментарий %q, ожидался созданный логин tv-…", code)
	}

	// /order/<id> обязан показывать тот же комментарий (приложение опрашивает его).
	for _, tc := range []struct{ id, want string }{{renewID, "alice"}, {firstID, code}} {
		resp, err := http.Get(srv.URL + "/order/" + tc.id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var got map[string]any
		err = json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("decode get: %v", err)
		}
		if got["code"] != tc.want {
			t.Fatalf("GET /order/%s: code %v, ожидался %q", tc.id, got["code"], tc.want)
		}
	}

	// Внутренний identity-код остаётся случайным и уникальным: он уходит в
	// control-plane orders.payment_code (UNIQUE), поэтому comment его не заменяет.
	for _, id := range []string{renewID, firstID} {
		o, err := ost.ByID(id)
		if err != nil {
			t.Fatalf("byID %s: %v", id, err)
		}
		if !strings.HasPrefix(o.Code, "M") || o.Code == o.Login {
			t.Fatalf("identity-код %q заказа %s должен остаться случайным M+5", o.Code, id)
		}
	}
}
