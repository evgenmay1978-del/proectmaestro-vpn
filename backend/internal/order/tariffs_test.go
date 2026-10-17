package order

import (
	"fmt"
	"testing"
)

func TestDefaultTariffsUseOwnerMonthlyPrice(t *testing.T) {
	want := []Tariff{
		{Key: "1m", Name: "1 месяц", Days: 30, Rub: 400},
		{Key: "2m", Name: "2 месяца", Days: 60, Rub: 800},
		{Key: "3m", Name: "3 месяца", Days: 90, Rub: 1200},
		{Key: "6m", Name: "6 месяцев", Days: 180, Rub: 2400},
		{Key: "12m", Name: "12 месяцев", Days: 365, Rub: 4800},
	}
	if got := fmt.Sprint(DefaultTariffs); got != fmt.Sprint(want) {
		t.Fatalf("DefaultTariffs = %v, want %v", DefaultTariffs, want)
	}
	for _, tariff := range DefaultTariffs {
		months := map[string]int{"1m": 1, "2m": 2, "3m": 3, "6m": 6, "12m": 12}[tariff.Key]
		if months == 0 {
			t.Fatalf("unexpected tariff key %q", tariff.Key)
		}
		if tariff.Rub != months*400 {
			t.Fatalf("%s price = %d ₽, want %d ₽", tariff.Key, tariff.Rub, months*400)
		}
	}
}
