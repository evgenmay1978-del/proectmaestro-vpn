package api

import (
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
)

// TestOrderNotifyText фиксирует, что владелец видит в уведомлении логин клиента
// (его пишут в комментарии к переводу), а код заказа остаётся вторым ключом.
func TestOrderNotifyText(t *testing.T) {
	cases := []struct {
		name    string
		login   string
		want    string
		notWant string
	}{
		{"действующий клиент", "wapmix", "🔑 Логин клиента: wapmix", "не привязан"},
		{"новый клиент (tv-)", "tv-L72BPCG7", "не привязан — новый клиент", "tv-L72BPCG7"},
		{"без логина", "", "не привязан — новый клиент", ""},
	}
	for _, c := range cases {
		o := &order.Order{ID: "ord_test", Tariff: "1m", Days: 30, Rub: 400, Code: "M5MH4L", Login: c.login, Status: "pending"}
		got := orderNotifyText(o)
		if !strings.Contains(got, c.want) {
			t.Fatalf("%s: нет %q в\n%s", c.name, c.want, got)
		}
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Fatalf("%s: лишнее %q в\n%s", c.name, c.notWant, got)
		}
		if !strings.Contains(got, "Код платежа: M5MH4L") || !strings.Contains(got, "Заказ: ord_test") {
			t.Fatalf("%s: потерялись код/заказ в\n%s", c.name, got)
		}
	}
}
